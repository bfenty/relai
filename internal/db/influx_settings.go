package db

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// InfluxSettings configures the optional export of routing metrics to InfluxDB v2.
//
// The token is stored but never returned; HasToken tells the UI whether one is set,
// and an empty token on update preserves the stored value. Same contract as backend
// provider keys, so a redacted value can be round-tripped safely.
type InfluxSettings struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Org      string `json:"org"`
	Bucket   string `json:"bucket"`
	Token    string `json:"token,omitempty"`
	HasToken bool   `json:"has_token"`
	// IntervalSeconds is how often a point is written. Each write covers the window
	// since the previous one, so this also sets the resolution of the stored series.
	IntervalSeconds int `json:"interval_seconds"`
}

// DefaultInfluxSettings points at a local InfluxDB v2 on its standard port, disabled
// until someone supplies a token.
func DefaultInfluxSettings() InfluxSettings {
	return InfluxSettings{
		Enabled:         false,
		URL:             "http://127.0.0.1:8086",
		Org:             "",
		Bucket:          "relai",
		IntervalSeconds: 60,
	}
}

const (
	minInfluxInterval = 10
	maxInfluxInterval = 3600
)

var influxKeys = []string{
	"influx_enabled", "influx_url", "influx_org", "influx_bucket",
	"influx_token", "influx_interval_seconds",
}

// GetInflux returns the stored settings with the token included. Handlers must
// redact it before responding; the exporter needs it.
func (r *SettingsRepo) GetInflux() (InfluxSettings, error) {
	s := DefaultInfluxSettings()
	rows, err := r.db.Query(
		`SELECT key, value FROM app_settings WHERE key IN (?,?,?,?,?,?)`,
		influxKeys[0], influxKeys[1], influxKeys[2], influxKeys[3], influxKeys[4], influxKeys[5])
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return s, err
		}
		switch k {
		case "influx_enabled":
			s.Enabled = v == "true"
		case "influx_url":
			s.URL = v
		case "influx_org":
			s.Org = v
		case "influx_bucket":
			s.Bucket = v
		case "influx_token":
			s.Token = v
		case "influx_interval_seconds":
			if n, err := strconv.Atoi(v); err == nil {
				s.IntervalSeconds = n
			}
		}
	}
	s.HasToken = s.Token != ""
	return s, rows.Err()
}

// UpdateInflux validates and stores the settings. An empty token preserves the
// stored one so a redacted round trip does not wipe it.
func (r *SettingsRepo) UpdateInflux(in InfluxSettings) (InfluxSettings, error) {
	in.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
	in.Org = strings.TrimSpace(in.Org)
	in.Bucket = strings.TrimSpace(in.Bucket)

	if in.IntervalSeconds == 0 {
		in.IntervalSeconds = DefaultInfluxSettings().IntervalSeconds
	}
	if in.IntervalSeconds < minInfluxInterval || in.IntervalSeconds > maxInfluxInterval {
		return InfluxSettings{}, fmt.Errorf("interval must be between %d and %d seconds",
			minInfluxInterval, maxInfluxInterval)
	}
	if in.Enabled {
		// Only insist on a usable configuration when it is actually being turned on,
		// so a half-filled form can still be saved.
		if in.URL == "" {
			return InfluxSettings{}, fmt.Errorf("url is required when export is enabled")
		}
		u, err := url.Parse(in.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return InfluxSettings{}, fmt.Errorf("url must be an absolute http(s) URL")
		}
		if in.Bucket == "" {
			return InfluxSettings{}, fmt.Errorf("bucket is required when export is enabled")
		}
	}

	existing, err := r.GetInflux()
	if err != nil {
		return InfluxSettings{}, err
	}
	token := strings.TrimSpace(in.Token)
	if token == "" {
		token = existing.Token
	}
	if in.Enabled && token == "" {
		return InfluxSettings{}, fmt.Errorf("token is required when export is enabled")
	}

	vals := map[string]string{
		"influx_enabled":          strconv.FormatBool(in.Enabled),
		"influx_url":              in.URL,
		"influx_org":              in.Org,
		"influx_bucket":           in.Bucket,
		"influx_token":            token,
		"influx_interval_seconds": strconv.Itoa(in.IntervalSeconds),
	}
	tx, err := r.db.Begin()
	if err != nil {
		return InfluxSettings{}, err
	}
	defer tx.Rollback()
	for k, v := range vals {
		if _, err := tx.Exec(
			`INSERT INTO app_settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return InfluxSettings{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return InfluxSettings{}, err
	}
	return r.GetInflux()
}
