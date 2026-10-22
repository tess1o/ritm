// Package config reads Ritm's configuration from environment variables (§4).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	InitUser             string // created on startup if it does not exist yet
	InitPassword         string
	RegistrationEnabled  bool
	DataDir              string
	ListenAddr           string
	BaseURL              string
	TZ                   *time.Location
	MaxUploadBytes       int64
	MaxConcurrentImports int
	SessionTTL           time.Duration
	DeltaWindow          time.Duration
	LogLevel             slog.Level
	StaleAfter           time.Duration
	// TrustedProxies are the peers whose X-Forwarded-For is believed; nil = loopback and private
	// addresses, empty = none.
	TrustedProxies []netip.Prefix
}

// Load reads the environment.
func Load() (*Config, error) {
	c := &Config{
		InitUser:     strings.TrimSpace(os.Getenv("RITM_INIT_USER")),
		InitPassword: os.Getenv("RITM_INIT_PASSWORD"),
		DataDir:      env("RITM_DATA_DIR", "/data"),
		ListenAddr:   env("RITM_LISTEN_ADDR", ":8080"),
		BaseURL:      strings.TrimRight(os.Getenv("RITM_BASE_URL"), "/"),
	}
	var errs []error
	if (c.InitUser == "") != (c.InitPassword == "") {
		errs = append(errs, errors.New("RITM_INIT_USER and RITM_INIT_PASSWORD must be set together"))
	}
	reg, err := strconv.ParseBool(env("RITM_REGISTRATION_ENABLED", "false"))
	if err != nil {
		errs = append(errs, errors.New("RITM_REGISTRATION_ENABLED: invalid boolean"))
	}
	c.RegistrationEnabled = reg

	tz, err := time.LoadLocation(env("RITM_TZ", "UTC"))
	if err != nil {
		errs = append(errs, fmt.Errorf("RITM_TZ: %w", err))
		tz = time.UTC
	}
	c.TZ = tz

	mb, err := strconv.ParseInt(env("RITM_MAX_UPLOAD_MB", "4096"), 10, 64)
	if err != nil || mb <= 0 {
		errs = append(errs, fmt.Errorf("RITM_MAX_UPLOAD_MB: invalid value"))
	}
	c.MaxUploadBytes = mb << 20

	n, err := strconv.Atoi(env("RITM_MAX_CONCURRENT_IMPORTS", "1"))
	if err != nil || n <= 0 {
		errs = append(errs, fmt.Errorf("RITM_MAX_CONCURRENT_IMPORTS: invalid value"))
	}
	c.MaxConcurrentImports = n

	durations := []struct {
		name, def string
		dst       *time.Duration
	}{
		{"RITM_SESSION_TTL", "720h", &c.SessionTTL},
		{"RITM_DELTA_WINDOW", "168h", &c.DeltaWindow},
		{"RITM_STALE_AFTER", "336h", &c.StaleAfter},
	}
	for _, d := range durations {
		v, err := ParseDuration(env(d.name, d.def))
		if err != nil || v < 0 {
			errs = append(errs, fmt.Errorf("%s: invalid duration", d.name))
		}
		*d.dst = v
	}

	if v := strings.TrimSpace(os.Getenv("RITM_TRUSTED_PROXIES")); v != "" {
		p, err := parseProxies(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("RITM_TRUSTED_PROXIES: %w", err))
		}
		c.TrustedProxies = p
	}

	if err := c.LogLevel.UnmarshalText([]byte(env("RITM_LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("RITM_LOG_LEVEL: %w", err))
	}
	return c, errors.Join(errs...)
}

// SecureCookies reports whether cookies should carry the Secure flag.
func (c *Config) SecureCookies() bool {
	return strings.HasPrefix(c.BaseURL, "https://")
}

// ParseDuration extends time.ParseDuration with a "d" (24h) unit, e.g. "10d" or "1d12h".
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "d"); i > 0 {
		days, err := strconv.ParseFloat(s[:i], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		d := time.Duration(days * float64(24*time.Hour))
		if rest := s[i+1:]; rest != "" {
			r, err := time.ParseDuration(rest)
			if err != nil {
				return 0, err
			}
			d += r
		}
		return d, nil
	}
	return time.ParseDuration(s)
}

// parseProxies parses "none" or a comma-separated list of IPs and CIDRs.
func parseProxies(v string) ([]netip.Prefix, error) {
	out := []netip.Prefix{}
	if strings.EqualFold(v, "none") {
		return out, nil
	}
	for _, f := range strings.Split(v, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			a, err := netip.ParseAddr(f)
			if err != nil {
				return nil, err
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
