package httpx

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	defer SetTrustedProxies(nil)
	req := func(peer, xff string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer + ":4321"
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return ClientIP(r)
	}
	cases := []struct {
		trusted         []netip.Prefix
		peer, xff, want string
	}{
		{nil, "127.0.0.1", "9.9.9.9, 1.2.3.4", "1.2.3.4"}, // last entry is the proxy's
		{nil, "172.18.0.5", "1.2.3.4", "172.18.0.5"},      // private peers are not trusted by default
		{nil, "8.8.8.8", "1.2.3.4", "8.8.8.8"},            // public peer: header ignored
		{nil, "127.0.0.1", "not-an-ip", "127.0.0.1"},
		{[]netip.Prefix{}, "127.0.0.1", "1.2.3.4", "127.0.0.1"},
		{[]netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}, "172.18.0.5", "1.2.3.4", "1.2.3.4"},
		{[]netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}, "192.168.1.20", "1.2.3.4", "192.168.1.20"},
	}
	for _, c := range cases {
		SetTrustedProxies(c.trusted)
		if got := req(c.peer, c.xff); got != c.want {
			t.Errorf("trusted %v, peer %s, xff %q: %s, want %s", c.trusted, c.peer, c.xff, got, c.want)
		}
	}
}
