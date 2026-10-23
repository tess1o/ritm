package api

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// minCompressSize is the smallest known-length response worth compressing.
const minCompressSize = 1024

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

// compress gzips text responses (JSON, the SPA's scripts and styles) for clients that accept it.
// Streams (import progress, MCP), range requests and binary files are sent as they are.
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" || r.URL.Path == "/mcp" || !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	for part := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		coding, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			return strings.ReplaceAll(strings.TrimSpace(q), " ", "") != "q=0"
		}
	}
	return false
}

func compressible(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	switch {
	case mt == "text/event-stream":
		return false
	case strings.HasPrefix(mt, "text/"), strings.HasSuffix(mt, "json"), strings.HasSuffix(mt, "+xml"),
		mt == "application/javascript", mt == "application/yaml":
		return true
	}
	return false
}

// gzipWriter holds back the status line until it knows whether to compress: a compressible
// response is buffered until it reaches minCompressSize (or is flushed), smaller ones go out as
// they are.
type gzipWriter struct {
	http.ResponseWriter
	code int // status waiting to be sent; 0 once sent
	buf  []byte
	gz   *gzip.Writer
	sent bool
	pass bool // not compressible: write straight through
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.sent || w.code != 0 {
		return
	}
	h := w.Header()
	if code < 200 || code == http.StatusNoContent || code == http.StatusPartialContent || code == http.StatusNotModified ||
		h.Get("Content-Encoding") != "" || !compressible(h.Get("Content-Type")) {
		w.pass, w.sent = true, true
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if n, err := strconv.Atoi(h.Get("Content-Length")); err == nil && n < minCompressSize {
		w.pass, w.sent = true, true
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.code = code
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.sent && w.code == 0 {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.WriteHeader(http.StatusOK)
	}
	switch {
	case w.pass:
		return w.ResponseWriter.Write(b)
	case w.gz != nil:
		return w.gz.Write(b)
	}
	w.buf = append(w.buf, b...)
	if len(w.buf) >= minCompressSize {
		if err := w.start(); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

// start sends the held status with Content-Encoding: gzip and compresses what was buffered.
func (w *gzipWriter) start() error {
	h := w.Header()
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	w.ResponseWriter.WriteHeader(w.code)
	w.code, w.sent = 0, true
	w.gz = gzipPool.Get().(*gzip.Writer)
	w.gz.Reset(w.ResponseWriter)
	_, err := w.gz.Write(w.buf)
	w.buf = nil
	return err
}

// finish sends a held small response uncompressed.
func (w *gzipWriter) finish() {
	if w.sent || w.code == 0 {
		return
	}
	w.ResponseWriter.WriteHeader(w.code)
	w.code, w.sent, w.pass = 0, true, true
	w.ResponseWriter.Write(w.buf)
	w.buf = nil
}

func (w *gzipWriter) Flush() {
	if !w.sent {
		if w.code == 0 {
			w.WriteHeader(http.StatusOK)
		}
		if !w.sent {
			w.start()
		}
	}
	if w.gz != nil {
		w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipWriter) close() {
	w.finish()
	if w.gz != nil {
		w.gz.Close()
		w.gz.Reset(io.Discard)
		gzipPool.Put(w.gz)
		w.gz = nil
	}
}
