package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/danielgtaylor/huma/v2/sse"

	"ritm/internal/auth"
	"ritm/internal/ingest"
	"ritm/internal/query"
)

// Import is an import with its live progress while it is queued or running.
type Import struct {
	ingest.ImportRow
	Progress *ingest.Progress `json:"progress,omitempty" doc:"Live progress; present while the import is queued or running"`
}

// uploadInput streams the request body itself: it has no Body field, so Huma does not read
// (and buffer) the upload, which can be several GB (ING-1).
type uploadInput struct {
	Full     bool   `query:"full" doc:"Full re-import: ignore what was imported before (DUP-3)"`
	FileName string `header:"X-File-Name" doc:"File name, URL-encoded (for raw bodies; multipart uses the part's file name)" example:"export.zip"`
	req      *http.Request
	w        http.ResponseWriter
}

func (u *uploadInput) Resolve(ctx huma.Context) []error {
	u.req, u.w = humago.Unwrap(ctx)
	return nil
}

type importCreated struct {
	Status   int
	Location string `header:"Location" doc:"URL of the import's status"`
	Body     Import
}

type importPath struct {
	ID string `path:"id" doc:"Import id (UUID)"`
}

var binary = &huma.Schema{Type: huma.TypeString, Format: "binary"}

func (s *Server) registerImports() {
	tags := []string{"Imports"}
	upload := huma.Operation{OperationID: "createImport", Method: http.MethodPost, Path: "/imports", Tags: tags,
		Summary: "Upload an Apple Health export", DefaultStatus: http.StatusAccepted,
		Description: "Upload `export.zip` (or a bare `export.xml`) from Health → profile → Export All Health Data, as the raw request body " +
			"(any content type; set `X-File-Name`) or as the first file of a multipart form. The body is streamed to disk and imported in " +
			"the background; only new data is stored (G2). Follow progress at `/imports/{id}` or `/imports/{id}/events`. " +
			"While another user's import holds the server-wide slot (RITM_MAX_CONCURRENT_IMPORTS), the import waits as `queued`. " +
			"Works with an iPhone Shortcut: Get Contents of URL, method POST, header `Authorization: Bearer <ingest key>`, request body File.",
		Errors: []int{http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge}}
	register(s, sessionOrKey(auth.ScopeIngest), upload, s.uploadImport)
	// Documented only after registering: an operation with a RequestBody makes Huma read and
	// buffer the body, while uploadImport streams it.
	s.api.OpenAPI().Paths[BasePath+"/imports"].Post.RequestBody = &huma.RequestBody{Required: true, Description: "The export file",
		Content: map[string]*huma.MediaType{
			"application/zip":          {Schema: binary},
			"application/xml":          {Schema: binary},
			"application/octet-stream": {Schema: binary},
			"multipart/form-data": {Schema: &huma.Schema{Type: huma.TypeObject,
				Properties: map[string]*huma.Schema{"file": binary}, Required: []string{"file"}}},
		}}

	register(s, sessionOnly, huma.Operation{OperationID: "listImports", Method: http.MethodGet, Path: "/imports", Tags: tags,
		Summary: "Import history", Description: "The 50 most recent imports with their summaries (PRG-2), newest first."},
		func(ctx context.Context, _ *struct{}) (*body[Items[ingest.ImportRow]], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			rows, err := t.Query.Imports(ctx, 50)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(Items[ingest.ImportRow]{Items: rows}), nil
		})

	register(s, sessionOrKey(auth.ScopeIngest), huma.Operation{OperationID: "getImport", Method: http.MethodGet, Path: "/imports/{id}", Tags: tags,
		Summary: "Import status", Description: "Status and summary of one import, with live progress while it runs (polling fallback of PRG-1).",
		Errors: []int{http.StatusNotFound}},
		func(ctx context.Context, in *importPath) (*body[Import], error) {
			t, err := s.tenant(ctx)
			if err != nil {
				return nil, err
			}
			imp, err := s.importStatus(ctx, t.Query, t.Imports, in.ID)
			if err != nil {
				return nil, s.problem(ctx, err)
			}
			return ok(imp), nil
		})

	events := huma.Operation{OperationID: "streamImportEvents", Method: http.MethodGet, Path: "/imports/{id}/events", Tags: tags,
		Summary: "Import progress stream", Description: "Server-sent events (PRG-1): `progress` while the import runs, then one `done` with the final import. " +
			"For an import that is not running, `done` (or `error`) is sent at once. Comments keep the connection alive every 15 s."}
	describe(&events, sessionOrKey(auth.ScopeIngest))
	sse.Register(s.v1, events, map[string]any{"progress": ingest.Progress{}, "done": Import{}, "error": huma.ErrorModel{}},
		func(ctx context.Context, in *importPath, send sse.Sender) {
			t, err := s.tenant(ctx)
			if err != nil {
				send.Data(huma.ErrorModel{Title: "Service Unavailable", Status: http.StatusServiceUnavailable, Detail: err.Error()})
				return
			}
			done := func() {
				if imp, err := s.importStatus(ctx, t.Query, t.Imports, in.ID); err == nil {
					send.Data(imp)
				} else {
					send.Data(huma.ErrorModel{Title: "Not Found", Status: http.StatusNotFound, Detail: "import not found"})
				}
			}
			ch, unsubscribe, running := t.Imports.Subscribe(in.ID)
			defer unsubscribe()
			if !running {
				done()
				return
			}
			heartbeat := time.NewTicker(15 * time.Second)
			defer heartbeat.Stop()
			for {
				select {
				case p, open := <-ch:
					if !open {
						done()
						return
					}
					send.Data(p)
				case <-heartbeat.C:
					send.Comment("keep-alive")
				case <-ctx.Done():
					return
				}
			}
		})
}

func (s *Server) importStatus(ctx context.Context, q *query.Service, m *ingest.Manager, id string) (Import, error) {
	row, err := q.Import(ctx, id)
	if err != nil {
		return Import{}, err
	}
	imp := Import{ImportRow: row}
	if p, ok := m.Live(id); ok && (row.Status == ingest.StatusRunning || row.Status == ingest.StatusQueued) {
		imp.Progress = &p
	}
	return imp, nil
}

// uploadImport streams the request body (raw or the first multipart file) to RITM_DATA_DIR/tmp
// and starts a background import (ING-1, ING-2).
func (s *Server) uploadImport(ctx context.Context, in *uploadInput) (*importCreated, error) {
	t, err := s.tenant(ctx)
	if err != nil {
		return nil, err
	}
	imports := t.Imports
	if err := imports.Reserve(); err != nil {
		return nil, huma.Error409Conflict(err.Error())
	}
	started := false
	defer func() {
		if !started {
			imports.Release()
		}
	}()
	r := in.req
	r.Body = http.MaxBytesReader(in.w, r.Body, s.Cfg.MaxUploadBytes)
	var (
		src  io.Reader = r.Body
		name           = in.FileName
	)
	if mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" && params["boundary"] != "" {
		mr, err := r.MultipartReader()
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				return nil, huma.Error400BadRequest("multipart body has no file part")
			}
			if part.FileName() != "" {
				src, name = part, part.FileName()
				break
			}
		}
	}
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		name = "export.zip"
	}
	id := ingest.NewID()
	tmp := filepath.Join(imports.TmpDir(), id)
	if err := os.MkdirAll(imports.TmpDir(), 0o750); err != nil {
		return nil, s.problem(ctx, err)
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, s.problem(ctx, err)
	}
	n, err := io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, huma.Error413RequestEntityTooLarge(fmt.Sprintf("upload exceeds %d MB", s.Cfg.MaxUploadBytes>>20))
		}
		return nil, huma.Error400BadRequest(fmt.Sprintf("upload interrupted: %v", err))
	}
	if n == 0 {
		os.Remove(tmp)
		return nil, huma.Error400BadRequest("empty upload")
	}
	started = true // Start releases the slot itself, also on failure
	if err := imports.Start(id, tmp, name, n, in.Full); err != nil {
		return nil, s.problem(ctx, err)
	}
	imp, err := s.importStatus(ctx, t.Query, imports, id)
	if err != nil {
		return nil, s.problem(ctx, err)
	}
	return &importCreated{Status: http.StatusAccepted, Location: BasePath + "/imports/" + id, Body: imp}, nil
}
