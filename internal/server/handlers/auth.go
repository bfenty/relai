package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bfenty/relai/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// AuthHandler handles login, logout, and session checks.
type AuthHandler struct {
	sessions      *db.SessionRepo
	adminCreds    *db.AdminCredRepo
	loginAttempts *db.LoginAttemptRepo
	secureCookies bool
}

// NewAuthHandler creates an AuthHandler.
func NewAuthHandler(sessions *db.SessionRepo, creds *db.AdminCredRepo, attempts *db.LoginAttemptRepo, secureCookies bool) *AuthHandler {
	return &AuthHandler{
		sessions:      sessions,
		adminCreds:    creds,
		loginAttempts: attempts,
		secureCookies: secureCookies,
	}
}

// HandleLogin authenticates with username+password and creates a session cookie.
func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := realIP(r)

	locked, err := h.loginAttempts.IsLocked(ip)
	if err != nil || locked {
		writeJSONError(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
		writeJSONError(w, "username and password required", http.StatusBadRequest)
		return
	}

	cred, err := h.adminCreds.Get(req.Username)
	if err != nil || cred == nil {
		h.loginAttempts.RecordFailure(ip)
		writeJSONError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(req.Password)); err != nil {
		h.loginAttempts.RecordFailure(ip)
		writeJSONError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	h.loginAttempts.Reset(ip)

	token, err := generateSessionToken()
	if err != nil {
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.sessions.Create(token, ip, 24*time.Hour); err != nil {
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "relai_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleLogout deletes the session and clears the cookie.
func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("relai_session"); err == nil {
		h.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "relai_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// HandleMe returns 200 if the caller is authenticated (used by the frontend as an auth check).
func (h *AuthHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"authenticated": true})
}

// ── helpers ──────────────────────────────────────────────────────────────────

func generateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func realIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.SplitN(ip, ",", 2)[0]
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}

// HashPassword bcrypt-hashes a plaintext password.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(h), err
}
