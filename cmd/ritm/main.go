// Command ritm is the single-binary Ritm server and CLI (§15):
//
//	ritm serve                               run the HTTP server (default)
//	ritm import --user NAME [--full] FILE    import export.zip / export.xml for a user
//	ritm recompute --user NAME               rebuild a user's derived tables and insights
//	ritm user list|add|passwd|suspend|unsuspend|delete ...   manage accounts
//	ritm backup DIR                          copy app.db and every user's database into DIR
//	ritm healthcheck                         probe /healthz (for container health checks)
//	ritm openapi [--json]                    print the REST API's OpenAPI description
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ritm/internal/api"
	"ritm/internal/auth"
	"ritm/internal/clock"
	"ritm/internal/config"
	"ritm/internal/httpx"
	"ritm/internal/ingest"
	"ritm/internal/store"
	"ritm/internal/tenant"
	"ritm/web"
)

var version = "dev"

func main() {
	cmd, args := "serve", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "import":
		err = importCmd(args)
	case "recompute":
		err = recompute(args)
	case "user":
		err = userCmd(args)
	case "backup":
		err = backup(args)
	case "healthcheck":
		err = healthcheck()
	case "openapi":
		err = openapiCmd(args)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const usage = `usage:
  ritm serve
  ritm import --user NAME [--full] FILE
  ritm recompute --user NAME
  ritm user list
  ritm user add NAME
  ritm user passwd NAME
  ritm user suspend NAME
  ritm user unsuspend NAME
  ritm user delete [--yes] NAME
  ritm backup DIR
  ritm healthcheck
  ritm openapi [--json]
  ritm version
`

type app struct {
	cfg     *config.Config
	log     *slog.Logger
	db      *store.DB // accounts (app.db)
	auth    *auth.Service
	tenants *tenant.Registry
}

func newApp(ctx context.Context) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	if cfg.TrustedProxies != nil {
		httpx.SetTrustedProxies(cfg.TrustedProxies)
	}
	db, err := store.OpenApp(ctx, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	authSvc := auth.NewService(db, clock.System{}, cfg.SessionTTL)
	return &app{cfg: cfg, log: log, db: db, auth: authSvc, tenants: tenant.NewRegistry(cfg, log, clock.System{}, authSvc)}, nil
}

// ensureInitUser creates RITM_INIT_USER if it is configured and does not exist yet.
func (a *app) ensureInitUser(ctx context.Context) error {
	if a.cfg.InitUser == "" {
		return nil
	}
	created, err := a.auth.EnsureUser(ctx, a.cfg.InitUser, a.cfg.InitPassword)
	if err != nil {
		return fmt.Errorf("init user: %w", err)
	}
	if created {
		a.log.Info("created init user", "user", a.cfg.InitUser)
	}
	return nil
}

// openUser opens the named user's data outside the server (CLI import/recompute).
func (a *app) openUser(ctx context.Context, name string) (*tenant.Tenant, error) {
	if name == "" {
		return nil, errors.New("--user is required")
	}
	if err := a.ensureInitUser(ctx); err != nil {
		return nil, err
	}
	u, err := a.auth.UserByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return tenant.Open(ctx, a.cfg, a.log, clock.System{}, u.User)
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx)
	if err != nil {
		return err
	}
	defer a.db.Close()

	if err := a.ensureInitUser(ctx); err != nil {
		return err
	}
	if err := a.tenants.OpenAll(ctx); err != nil {
		return fmt.Errorf("open user data: %w", err)
	}
	handler := api.New(api.Deps{
		Cfg: a.cfg, AppDB: a.db, Auth: a.auth, Tenants: a.tenants, Clock: clock.System{},
		Log: a.log, SPA: web.Dist(), Version: version,
	})
	srv := &http.Server{Addr: a.cfg.ListenAddr, Handler: handler, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute,
		MaxHeaderBytes: 64 << 10}
	ln, err := net.Listen("tcp", a.cfg.ListenAddr)
	if err != nil {
		return err
	}
	a.log.Info("ritm listening", "addr", ln.Addr().String(), "version", version, "data_dir", a.cfg.DataDir,
		"registration_enabled", a.cfg.RegistrationEnabled)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	a.log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Cancel imports first (they are marked failed, NFR-7) so their progress streams end, then
	// drain HTTP requests, and only then close the databases they use.
	a.tenants.StopImports(shutCtx)
	err = srv.Shutdown(shutCtx)
	a.tenants.Shutdown(shutCtx)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func importCmd(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	full := fs.Bool("full", false, "ignore high-water marks (full re-import)")
	user := fs.String("user", "", "username to import into")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: ritm import --user NAME [--full] FILE")
	}
	path := fs.Arg(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx)
	if err != nil {
		return err
	}
	defer a.db.Close()
	t, err := a.openUser(ctx, *user)
	if err != nil {
		return err
	}
	defer t.Close(context.Background())
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	opts := t.IngestOptions()
	opts.Full = *full
	last := time.Time{}
	opts.Progress = func(consumed, total int64) {
		if time.Since(last) > 2*time.Second && total > 0 {
			last = time.Now()
			fmt.Fprintf(os.Stderr, "\r%5.1f%%", float64(consumed)*100/float64(total))
		}
	}
	st, err := ingest.RunRecorded(ctx, t.DB, path, filepath.Base(path), fi.Size(), opts, t.Derive.AfterImport)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return err
	}
	fmt.Printf("imported in %.1fs: %d new workouts, %d days re-aggregated, earliest affected day %s\n",
		float64(st.DurationMS)/1000, st.NewWorkouts, st.DaysReaggregated, st.EarliestDay)
	for t, s := range st.Types {
		fmt.Printf("  %-60s scanned %8d inserted %8d duplicate %8d below_cutoff %8d skipped %d\n",
			strings.TrimPrefix(strings.TrimPrefix(t, "HKQuantityTypeIdentifier"), "HKCategoryTypeIdentifier"),
			s.Scanned, s.Inserted, s.Duplicate, s.BelowCutoff, s.Skipped)
	}
	for _, w := range st.Warnings {
		fmt.Println("warning:", w)
	}
	return nil
}

func recompute(args []string) error {
	fs := flag.NewFlagSet("recompute", flag.ExitOnError)
	user := fs.String("user", "", "username whose data to recompute")
	fs.Parse(args)
	ctx := context.Background()
	a, err := newApp(ctx)
	if err != nil {
		return err
	}
	defer a.db.Close()
	t, err := a.openUser(ctx, *user)
	if err != nil {
		return err
	}
	defer t.Close(ctx)
	return t.Derive.Recompute(ctx, "")
}

func healthcheck() error {
	cfg, _ := config.Load()
	addr := cfg.ListenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}

// openapiCmd prints the OpenAPI description (YAML by default); docs/openapi.yaml is its output.
func openapiCmd(args []string) error {
	fs := flag.NewFlagSet("openapi", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON instead of YAML")
	fs.Parse(args)
	spec := api.OpenAPI(version)
	var (
		b   []byte
		err error
	)
	if *asJSON {
		b, err = json.MarshalIndent(spec, "", "  ")
	} else {
		b, err = spec.YAML()
	}
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(b)
	return err
}
