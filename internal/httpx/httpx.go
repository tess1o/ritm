// Package httpx holds small HTTP helpers shared by handlers and middleware.
package httpx

import (
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes {"error": msg} with the given status.
func Error(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// trustedProxies are the peers whose X-Forwarded-For header is believed; nil means loopback and
// private addresses (a reverse proxy on the same host or Docker network).
var trustedProxies []netip.Prefix

// SetTrustedProxies sets the peers whose X-Forwarded-For header is honoured (RITM_TRUSTED_PROXIES).
// Call it once at startup. An empty, non-nil slice trusts no proxy.
func SetTrustedProxies(p []netip.Prefix) { trustedProxies = p }

func trusted(ip netip.Addr) bool {
	if trustedProxies == nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	for _, p := range trustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP returns the caller's IP. X-Forwarded-For is honoured only when the direct peer is a
// trusted proxy; its last entry (the one that proxy appended) is used.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip, err := netip.ParseAddr(host); err == nil && trusted(ip.Unmap()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return last
			}
		}
	}
	return host
}
