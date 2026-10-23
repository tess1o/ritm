package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompress(t *testing.T) {
	s := newTestServer(t, false, time.Now())
	get := func(path string, headers map[string]string) *httptest.ResponseRecorder {
		return s.raw("GET", path, nil, headers)
	}
	gz := map[string]string{"Accept-Encoding": "br, gzip"}

	w := get(BasePath+"/openapi.json", gz)
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Content-Length") != "" {
		t.Fatalf("openapi.json: %d %v", w.Code, w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil || !strings.Contains(string(b), `"openapi"`) {
		t.Fatalf("decompressed body: %v %.80s", err, b)
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary: %q", w.Header().Get("Vary"))
	}

	if w := get(BasePath+"/openapi.json", nil); w.Header().Get("Content-Encoding") != "" || !strings.Contains(w.Body.String(), `"openapi"`) {
		t.Fatalf("without Accept-Encoding: %v", w.Header())
	}
	if w := get(BasePath+"/openapi.json", map[string]string{"Accept-Encoding": "gzip;q=0"}); w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("gzip;q=0: %v", w.Header())
	}
	if w := get("/healthz", gz); w.Header().Get("Content-Encoding") != "" { // tiny body
		t.Fatalf("healthz: %v", w.Header())
	}
}

func TestCompressible(t *testing.T) {
	for ct, want := range map[string]bool{
		"application/json":               true,
		"application/problem+json":       true,
		"text/html; charset=utf-8":       true,
		"text/javascript; charset=utf-8": true,
		"image/svg+xml":                  true,
		"application/manifest+json":      true,
		"text/event-stream":              false,
		"image/png":                      false,
		"application/zip":                false,
		"":                               false,
	} {
		if got := compressible(ct); got != want {
			t.Errorf("compressible(%q) = %v, want %v", ct, got, want)
		}
	}
}
