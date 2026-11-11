package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/proxy"
)

// adminKeyMatches compares a presented token to the configured admin key in
// constant time. An empty configured key never matches: otherwise a
// deployment without RELAI_ADMIN_KEY would accept any bearer token.
func adminKeyMatches(adminKey, presented string) bool {
	if adminKey == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(adminKey), []byte(presented)) == 1
}

// keyCtxKey is the context key for the API key.
type keyCtxKey struct{}

// APIKeyFromContext returns the validated API key from the context.
func APIKeyFromContext(ctx context.Context) *db.APIKey {
	if k, ok := ctx.Value(keyCtxKey{}).(*db.APIKey); ok {
		return k
	}
	return nil
}

// Auth validates the API key from the Authorization header.
// If adminKey is non-empty and the token matches it, a synthetic unlimited key is injected.
// A valid session cookie also injects a synthetic unlimited key (allows GUI chat/benchmark).
func Auth(apiKeys *db.APIKeyRepo, adminKey string, sessions *db.SessionRepo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Session cookie → synthetic unlimited key
			if sessions != nil {
				if cookie, err := r.Cookie("relai_session"); err == nil {
					if valid, _ := sessions.Validate(cookie.Value); valid {
						ctx := context.WithValue(r.Context(), keyCtxKey{}, &db.APIKey{IsActive: true})
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}

			auth := r.Header.Get("Authorization")
			if auth == "" {
				auth = r.Header.Get("api-key")
			}
			if auth == "" {
				writeError(w, "missing API key", http.StatusUnauthorized)
				return
			}

			key := strings.TrimPrefix(auth, "Bearer ")

			if adminKeyMatches(adminKey, key) {
				ctx := context.WithValue(r.Context(), keyCtxKey{}, &db.APIKey{IsActive: true})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			apiKey, err := apiKeys.Validate(key)
			if err != nil {
				writeError(w, "invalid API key", http.StatusUnauthorized)
				return
			}

			allowed, err := apiKeys.CheckRateLimit(apiKey.ID)
			if err != nil {
				writeError(w, "rate limit check failed", http.StatusInternalServerError)
				return
			}
			if !allowed {
				writeError(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			ctx := context.WithValue(r.Context(), keyCtxKey{}, apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminAuth protects admin endpoints. Accepts a valid session cookie or Bearer admin key.
func AdminAuth(adminKey string, sessions *db.SessionRepo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Session cookie
			if sessions != nil {
				if cookie, err := r.Cookie("relai_session"); err == nil {
					if valid, _ := sessions.Validate(cookie.Value); valid {
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// Bearer admin key fallback (for API/CLI access)
			if auth := r.Header.Get("Authorization"); auth != "" {
				if adminKeyMatches(adminKey, strings.TrimPrefix(auth, "Bearer ")) {
					next.ServeHTTP(w, r)
					return
				}
			}

			writeError(w, "authentication required", http.StatusUnauthorized)
		})
	}
}

// SecurityHeaders adds standard security headers to every response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// CORS adds CORS headers for browser access (used on proxy routes for external clients).
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, api-key, MCP-Protocol-Version, MCP-Session-Id, Mcp-Method, Mcp-Name, Last-Event-ID")
		w.Header().Set("Access-Control-Expose-Headers", "MCP-Protocol-Version, MCP-Session-Id")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// MaxBody caps request body size to protect against memory exhaustion: the
// proxy and admin handlers buffer bodies in full.
func MaxBody(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(proxy.FormatError(message, status))
}
