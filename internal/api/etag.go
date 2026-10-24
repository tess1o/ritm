package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"ritm/internal/auth"
)

// uncached are data reads whose answer also depends on the clock or on storage details, so a
// data version cannot vouch for them.
var uncached = map[string]bool{"getFreshness": true, "getOverview": true, "getCoverage": true}

// bootID changes on every start: builds from a branch share a version ("main", "dev"), and a
// response cached by an older build may have another shape.
var bootID = time.Now().UnixNano()

// revalidate gives data reads an ETag, so clients can cache them and revalidate cheaply. The tag
// covers the user's database and its data version (which every change bumps), today in the
// user's time zone (relative periods move with it), the server version and start, the request URL, and
// is computed before the handler runs: a matching If-None-Match is answered 304 straight away.
func (s *Server) revalidate(ctx huma.Context, next func(huma.Context)) {
	op := ctx.Operation()
	a, _ := op.Metadata[accessKey].(access)
	if op.Method != http.MethodGet || !slices.Contains(a.scopes, auth.ScopeRead) || uncached[op.OperationID] {
		next(ctx)
		return
	}
	t, err := s.Tenants.Get(ctx.Context(), user(ctx.Context()))
	if err != nil {
		next(ctx)
		return
	}
	version, err := t.Query.DataVersion(ctx.Context())
	if err != nil {
		next(ctx)
		return
	}
	r, _ := humago.Unwrap(ctx)
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%s|%s|%d|%s", t.User.DBName, version, t.Query.Today(), s.Version, bootID, r.URL.RequestURI()))
	tag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
	ctx.SetHeader("ETag", tag)
	ctx.SetHeader("Cache-Control", "private, no-cache")
	if etagMatch(ctx.Header("If-None-Match"), tag) {
		ctx.SetStatus(http.StatusNotModified)
		return
	}
	next(ctx)
}

// etagMatch reports whether an If-None-Match header lists tag (weak comparison).
func etagMatch(header, tag string) bool {
	weak := strings.TrimPrefix(tag, "W/")
	for c := range strings.SplitSeq(header, ",") {
		c = strings.TrimSpace(c)
		if c == "*" || strings.TrimPrefix(c, "W/") == weak {
			return true
		}
	}
	return false
}
