// Package httpx holds small HTTP helpers shared by handlers and middleware.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
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

// trustedProxies are the peers whose X-Forwarded-For header is believed; nil means loopback only
// (a reverse proxy on the same host). A proxy on a Docker network must be listed explicitly: with
// some port-forwarding setups every internet client arrives from a private address.
var trustedProxies []netip.Prefix

var warnUntrusted sync.Once

// SetTrustedProxies sets the peers whose X-Forwarded-For header is honoured (RITM_TRUSTED_PROXIES).
// Call it once at startup. An empty, non-nil slice trusts no proxy.
func SetTrustedProxies(p []netip.Prefix) { trustedProxies = p }

func trusted(ip netip.Addr) bool {
	if trustedProxies == nil {
		return ip.IsLoopback()
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
	xff := r.Header.Get("X-Forwarded-For")
	ip, err := netip.ParseAddr(host)
	if err != nil || xff == "" {
		return host
	}
	ip = ip.Unmap()
	if !trusted(ip) {
		if ip.IsPrivate() {
			warnUntrusted.Do(func() {
				slog.Warn("ignoring X-Forwarded-For from an untrusted proxy; set RITM_TRUSTED_PROXIES to its address",
					"peer", ip.String())
			})
		}
		return host
	}
	parts := strings.Split(xff, ",")
	if last, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
		return last.Unmap().String()
	}
	return host
}
