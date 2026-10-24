package config

import (
	"net/netip"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"72h":   72 * time.Hour,
		"10d":   240 * time.Hour,
		"1d12h": 36 * time.Hour,
		"1.5d":  36 * time.Hour,
		" 14d ": 336 * time.Hour,
	}
	for in, want := range cases {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "d", "xd", "10dd", "ten"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q): want an error", bad)
		}
	}
}

func TestParseProxies(t *testing.T) {
	p, err := parseProxies("10.0.0.1, 192.168.0.0/16,::ffff:172.17.0.2")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.1/32", "192.168.0.0/16", "172.17.0.2/32"}
	if len(p) != len(want) {
		t.Fatalf("got %v", p)
	}
	for i, w := range want {
		if p[i] != netip.MustParsePrefix(w) {
			t.Errorf("prefix %d = %v, want %s", i, p[i], w)
		}
	}
	if p, err := parseProxies("none"); err != nil || p == nil || len(p) != 0 {
		t.Fatalf("none: %v %v (want an empty, non-nil list)", p, err)
	}
	if _, err := parseProxies("10.0.0.300"); err == nil {
		t.Fatal("invalid address accepted")
	}
}

func TestLoadValidatesPairsAndValues(t *testing.T) {
	t.Setenv("RITM_INIT_USER", "admin")
	t.Setenv("RITM_INIT_PASSWORD", "")
	t.Setenv("RITM_STALE_AFTER", "-1h")
	if _, err := Load(); err == nil {
		t.Fatal("want errors for a user without password and a negative duration")
	}
	t.Setenv("RITM_INIT_PASSWORD", "password1")
	t.Setenv("RITM_STALE_AFTER", "10d")
	t.Setenv("RITM_BASE_URL", "https://ritm.example/")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.StaleAfter != 240*time.Hour || c.BaseURL != "https://ritm.example" || !c.SecureCookies() || c.TrustedProxies != nil ||
		c.ShortcutURL != DefaultShortcutURL {
		t.Fatalf("config %+v", c)
	}
	t.Setenv("RITM_SHORTCUT_URL", "http://example.com/shortcut")
	if _, err := Load(); err == nil {
		t.Fatal("want an error for a non-https shortcut URL")
	}
	t.Setenv("RITM_SHORTCUT_URL", "https://www.icloud.com/shortcuts/abc")
	if c, err := Load(); err != nil || c.ShortcutURL != "https://www.icloud.com/shortcuts/abc" {
		t.Fatalf("shortcut URL: %v %v", c, err)
	}
}
