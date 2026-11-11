package db

import (
	"database/sql"
	"regexp"
	"strings"
)

type BrandingSettings struct {
	AppName      string `json:"app_name"`
	LogoURL      string `json:"logo_url"`
	PrimaryColor string `json:"primary_color"`
	AccentColor  string `json:"accent_color"`
}

type SettingsRepo struct {
	db *sql.DB
}

func newSettingsRepo(db *sql.DB) *SettingsRepo {
	return &SettingsRepo{db: db}
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func DefaultBrandingSettings() BrandingSettings {
	return BrandingSettings{
		AppName:      "Relai",
		LogoURL:      "",
		PrimaryColor: "#2563eb",
		AccentColor:  "#60a5fa",
	}
}

func (r *SettingsRepo) GetBranding() (BrandingSettings, error) {
	settings := DefaultBrandingSettings()
	rows, err := r.db.Query(`SELECT key, value FROM app_settings WHERE key IN ('app_name', 'logo_url', 'primary_color', 'accent_color')`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return settings, err
		}
		switch key {
		case "app_name":
			if value != "" {
				settings.AppName = value
			}
		case "logo_url":
			settings.LogoURL = value
		case "primary_color":
			if value != "" {
				settings.PrimaryColor = value
			}
		case "accent_color":
			if value != "" {
				settings.AccentColor = value
			}
		}
	}
	return settings, rows.Err()
}

func (r *SettingsRepo) UpdateBranding(input BrandingSettings) (BrandingSettings, error) {
	settings := normalizeBranding(input)
	values := map[string]string{
		"app_name":      settings.AppName,
		"logo_url":      settings.LogoURL,
		"primary_color": settings.PrimaryColor,
		"accent_color":  settings.AccentColor,
	}
	for key, value := range values {
		if _, err := r.db.Exec(`
			INSERT INTO app_settings (key, value, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
		`, key, value); err != nil {
			return settings, err
		}
	}
	return r.GetBranding()
}

func normalizeBranding(input BrandingSettings) BrandingSettings {
	defaults := DefaultBrandingSettings()
	appName := strings.TrimSpace(input.AppName)
	if appName == "" {
		appName = defaults.AppName
	}
	if len(appName) > 80 {
		appName = appName[:80]
	}

	logoURL := strings.TrimSpace(input.LogoURL)
	if len(logoURL) > 500000 {
		logoURL = logoURL[:500000]
	}
	if logoURL != "" && !strings.HasPrefix(logoURL, "https://") && !strings.HasPrefix(logoURL, "http://") && !strings.HasPrefix(logoURL, "data:image/") {
		logoURL = ""
	}

	primary := normalizeHexColor(input.PrimaryColor, defaults.PrimaryColor)
	accent := normalizeHexColor(input.AccentColor, defaults.AccentColor)
	return BrandingSettings{
		AppName:      appName,
		LogoURL:      logoURL,
		PrimaryColor: primary,
		AccentColor:  accent,
	}
}

func normalizeHexColor(value, fallback string) string {
	value = strings.TrimSpace(value)
	if hexColorRE.MatchString(value) {
		return value
	}
	return fallback
}
