// Package mcpserver is Ritm's MCP server: read-only tools, resources and prompts over the
// signed-in key owner's database, served as streamable HTTP at /mcp.
//
// The handler runs in stateless mode: every POST gets a temporary session on the server of the
// user its API key belongs to, so a session can never be reused with another user's key.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ritm/internal/auth"
	"ritm/internal/config"
	"ritm/internal/httpx"
	"ritm/internal/tenant"
)

const maxBody = 1 << 20

const instructions = `Ritm holds one person's Apple Watch / Apple Health history: daily activity (steps, distance,
calories, stand, exercise, rings), heart metrics (resting HR, HRV, walking HR, VO2max, SpO2, daily HR range),
sleep, workouts (mostly strength training) with HR zones and training load, and rule-based insights that
compare recent periods with the person's own baseline.

Conventions:
- Dates are YYYY-MM-DD in the user's time zone. Range tools take "period" (today, yesterday, last_7d,
  last_30d, last_90d, this_week, last_week, this_month, last_month, ytd, last_<n>d, YYYY-MM-DD or
  FROM..TO) or "from"/"to".
- Units are in field names (_bpm, _ms for HRV, _kcal, _m, _min, _s, _h, _pct). Times are local
  "YYYY-MM-DD HH:MM". A missing field means no data.
- Long ranges are returned per week or month (per-day averages over days with data, plus "days").
- Lists are capped; "truncated": true means narrow the range or raise "limit".
- Start with get_overview; ritm://data-coverage shows what data exists.
Everything is read-only. Describe findings factually; this is not medical advice.`

// Deps are what the server needs from the rest of the app.
type Deps struct {
	Cfg     *config.Config
	Tenants *tenant.Registry
	Log     *slog.Logger
	Version string
}

// Handler serves MCP for requests that passed auth.RequireKey(auth.ScopeRead).
type Handler struct {
	deps Deps
	h    http.Handler
}

func New(d Deps) *Handler {
	h := &Handler{deps: d}
	h.h = mcp.NewStreamableHTTPHandler(h.serverFor, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Requests carry a bearer key, so DNS rebinding cannot reach data; the check would
		// otherwise reject a reverse proxy on 127.0.0.1 forwarding the public Host header.
		DisableLocalhostProtection: true,
	})
	return h
}

type serverKey struct{}

// serverMemoKey caches a tenant's MCP server on the tenant (see tenant.Memo), so it is rebuilt
// after the user's data is reset and released when the user is deleted.
type serverMemoKey struct{}

// ServeHTTP opens the key owner's data once per request and hands the SDK the server bound to
// it; when the data cannot be opened the client gets 503 rather than the SDK's 400.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k, ok := auth.APIKeyFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	t, err := h.deps.Tenants.Get(r.Context(), k.User)
	if err != nil {
		h.deps.Log.Error("mcp: open user data", "user", k.User.Username, "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "data unavailable, try again later")
		return
	}
	s := t.Memo(serverMemoKey{}, func() any { return newServer(h.deps, t) }).(*mcp.Server)
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	h.h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverKey{}, s)))
}

// serverFor returns the server chosen in ServeHTTP (the SDK may call it more than once).
func (h *Handler) serverFor(r *http.Request) *mcp.Server {
	s, _ := r.Context().Value(serverKey{}).(*mcp.Server)
	return s
}

func newServer(d Deps, t *tenant.Tenant) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "ritm", Title: "Ritm", Version: d.Version},
		&mcp.ServerOptions{Instructions: instructions})
	tl := &tools{deps: d, t: t}
	tl.register(s)
	tl.registerResources(s)
	registerPrompts(s)
	return s
}

// addTool registers a read-only tool whose handler returns any JSON-able value; the value is
// shaped (see shape) and sent as one text block.
func addTool[In any](s *mcp.Server, t *tools, name, title, desc string, fn func(context.Context, In) (any, error)) {
	closed := false
	mcp.AddTool(s, &mcp.Tool{Name: name, Title: title, Description: desc,
		Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			if t.t == nil {
				return nil, nil, fmt.Errorf("no data available")
			}
			v, err := fn(ctx, in)
			if err != nil {
				return nil, nil, err
			}
			text, err := t.encode(v)
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
		})
}

func (t *tools) encode(v any) (string, error) {
	a, err := toAny(v)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(shape(a, t.t.Zone.Location()))
	return string(b), err
}

// Catalog describes the tools, resources and prompts for the setup page in the UI. It lists
// them through a real client session, so the page always matches what assistants see.
type Catalog struct {
	Tools     []CatalogTool  `json:"tools"`
	Resources []CatalogEntry `json:"resources"`
	Prompts   []CatalogEntry `json:"prompts"`
}

type CatalogTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Params      []CatalogParam `json:"params"`
}

type CatalogParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type CatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

var (
	catalogOnce sync.Once
	catalog     *Catalog
	catalogErr  error
)

func (h *Handler) Catalog() (*Catalog, error) {
	catalogOnce.Do(func() { catalog, catalogErr = buildCatalog(context.Background(), h.deps) })
	return catalog, catalogErr
}

func buildCatalog(ctx context.Context, d Deps) (*Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	srv := newServer(d, nil)
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		return nil, err
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "ritm-catalog", Version: d.Version}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return nil, err
	}
	defer cs.Close()

	c := &Catalog{}
	tl, err := cs.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, t := range tl.Tools {
		ct := CatalogTool{Name: t.Name, Title: t.Title, Description: t.Description, Params: []CatalogParam{}}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if b, err := json.Marshal(t.InputSchema); err == nil {
			_ = json.Unmarshal(b, &schema)
		}
		required := map[string]bool{}
		for _, r := range schema.Required {
			required[r] = true
		}
		for name, p := range schema.Properties {
			ct.Params = append(ct.Params, CatalogParam{Name: name, Description: p.Description, Required: required[name]})
		}
		sortParams(ct.Params)
		c.Tools = append(c.Tools, ct)
	}
	slices.SortStableFunc(c.Tools, func(a, b CatalogTool) int {
		return slices.Index(toolOrder, a.Name) - slices.Index(toolOrder, b.Name)
	})
	rl, err := cs.ListResources(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, r := range rl.Resources {
		c.Resources = append(c.Resources, CatalogEntry{Name: r.URI, Description: r.Description})
	}
	pl, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, p := range pl.Prompts {
		c.Prompts = append(c.Prompts, CatalogEntry{Name: p.Name, Description: p.Description})
	}
	return c, nil
}
