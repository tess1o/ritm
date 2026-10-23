//go:build unix

// Command bench checks Ritm against its performance budgets using a built binary:
//
//	peak RSS while importing (≤ 150 MB for a 2 GB export.xml)
//	first import (≤ 120 s per 1 GB export.xml) and delta re-import (≤ 60 s)
//	API p95 (≤ 200 ms) for 5-year ranges
//	idle server memory (≤ 40 MB)
//
// It runs the binary in a throw-away data directory and never touches ./data:
//
//	go run ./tools/bench -bin bin/ritm -export ~/Downloads/export.zip
//	go run ./tools/bench -bin bin/ritm -days 1825 -hr 40      # synthetic ≈ 2 GB export.xml
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"ritm/internal/synth"
)

const (
	user     = "bench"
	password = "bench-password"
)

type result struct {
	name, value, limit string
	ok                 bool
}

func main() {
	bin := flag.String("bin", "bin/ritm", "ritm binary to benchmark")
	export := flag.String("export", "", "existing export.zip/export.xml (default: generate a synthetic one)")
	nDays := flag.Int("days", 1825, "days of synthetic data")
	hr := flag.Int("hr", 300, "synthetic passive heart-rate interval in seconds (lower = bigger export)")
	requests := flag.Int("requests", 20, "timed requests per API endpoint")
	keep := flag.Bool("keep", false, "keep the work directory")
	flag.Parse()

	if err := run(*bin, *export, *nDays, *hr, *requests, *keep); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

func run(bin, export string, nDays, hr, requests int, keep bool) error {
	bin, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("%w (run `make build-go` first)", err)
	}
	work, err := os.MkdirTemp("", "ritm-bench-")
	if err != nil {
		return err
	}
	if keep {
		fmt.Println("work dir:", work)
	} else {
		defer os.RemoveAll(work)
	}

	if export == "" {
		export = filepath.Join(work, "export.zip")
		loc, _ := time.LoadLocation("Europe/Kyiv")
		last := time.Now().In(loc).AddDate(0, 0, -1)
		start := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -nDays+1)
		fmt.Printf("generating %d days of synthetic data (HR every %d s)…\n", nDays, hr)
		if err := synth.WriteZip(export, synth.Options{Start: start, Days: nDays, Loc: loc, Seed: 1, Sleep: true, IPhone: true, HRIntervalSec: hr}); err != nil {
			return err
		}
	}
	xmlBytes, err := xmlSize(export)
	if err != nil {
		return err
	}
	gb := float64(xmlBytes) / 1e9
	fmt.Printf("export.xml: %.2f GB\n", gb)

	addr, err := freeAddr()
	if err != nil {
		return err
	}
	env := []string{"RITM_DATA_DIR=" + filepath.Join(work, "data"), "RITM_INIT_USER=" + user, "RITM_INIT_PASSWORD=" + password,
		"RITM_LOG_LEVEL=warn", "RITM_LISTEN_ADDR=" + addr, "RITM_TZ=Europe/Kyiv"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "RITM_") {
			env = append(env, kv)
		}
	}

	var res []result
	fmt.Println("first import…")
	first, rss, err := timed(bin, env, "import", "--user", user, export)
	if err != nil {
		return err
	}
	perGB := first.Seconds() / gb
	rssLimit := 150.0
	res = append(res,
		result{"first import", fmt.Sprintf("%.1f s (%.1f s per GB)", first.Seconds(), perGB), "≤ 120 s per GB", perGB <= 120},
		result{"peak RSS, import", fmt.Sprintf("%.0f MB", rss), "≤ 150 MB (2 GB export)", rss <= rssLimit})

	fmt.Println("delta re-import of the same export…")
	delta, rss2, err := timed(bin, env, "import", "--user", user, export)
	if err != nil {
		return err
	}
	res = append(res,
		result{"delta re-import", fmt.Sprintf("%.1f s", delta.Seconds()), "≤ 60 s", delta.Seconds() <= 60},
		result{"peak RSS, re-import", fmt.Sprintf("%.0f MB", rss2), "≤ 150 MB", rss2 <= rssLimit})

	fmt.Println("serving…")
	srv := exec.Command(bin, "serve")
	srv.Env = env
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		return err
	}
	defer func() {
		srv.Process.Signal(syscall.SIGTERM)
		srv.Wait()
	}()
	base := "http://" + addr
	if err := waitHealthy(base); err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	idle, err := rssOf(srv.Process.Pid)
	if err != nil {
		return err
	}
	res = append(res, result{"idle RSS, server", fmt.Sprintf("%.0f MB", idle), "≤ 40 MB", idle <= 40})

	p95s, err := apiLatency(base, requests)
	if err != nil {
		return err
	}
	for _, p := range p95s {
		res = append(res, result{"p95 " + p.path, fmt.Sprintf("%.0f ms", p.ms), "≤ 200 ms", p.ms <= 200})
	}
	busy, _ := rssOf(srv.Process.Pid)
	res = append(res, result{"RSS after API load (info)", fmt.Sprintf("%.0f MB", busy), "—", true})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\ncheck\tvalue\tlimit\tresult")
	failed := 0
	for _, r := range res {
		mark := "PASS"
		if !r.ok {
			mark, failed = "FAIL", failed+1
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.name, r.value, r.limit, mark)
	}
	w.Flush()
	fmt.Printf("\n%s/%s, %d CPUs, export.xml %.2f GB\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), gb)
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}

// timed runs the binary and returns its wall time and peak RSS in MB.
func timed(bin string, env []string, args ...string) (time.Duration, float64, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	if err := cmd.Run(); err != nil {
		return 0, 0, fmt.Errorf("%s: %w\n%s", strings.Join(args, " "), err, out.String())
	}
	d := time.Since(start)
	ru := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	maxrss := float64(ru.Maxrss) * 1024 // kilobytes on Linux
	if runtime.GOOS == "darwin" {
		maxrss = float64(ru.Maxrss) // bytes on macOS
	}
	return d, maxrss / (1 << 20), nil
}

func rssOf(pid int) (float64, error) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return kb / 1024, err
}

func xmlSize(path string) (int64, error) {
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		fi, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "export.xml") {
			return int64(f.UncompressedSize64), nil
		}
	}
	return 0, errors.New("no export.xml in " + path)
}

func freeAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func waitHealthy(base string) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/healthz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("server did not become healthy")
}

type p95 struct {
	path string
	ms   float64
}

// apiLatency signs in and times the SPA's heaviest endpoints over 5-year ranges.
func apiLatency(base string, n int) ([]p95, error) {
	body, _ := json.Marshal(map[string]string{"username": user, "password": password})
	req, _ := http.NewRequest("POST", base+"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ritm-CSRF", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "ritm_session" {
			cookie = c
		}
	}
	if cookie == nil {
		return nil, fmt.Errorf("login failed: %s", resp.Status)
	}

	var cov struct {
		Types []struct{ To string } `json:"types"`
	}
	if err := getJSON(base+"/api/v1/coverage", cookie, &cov); err != nil {
		return nil, err
	}
	to := "" // latest day with data
	for _, t := range cov.Types {
		to = max(to, t.To)
	}
	if to == "" {
		return nil, errors.New("no data imported")
	}
	end, _ := time.Parse("2006-01-02", to)
	rng := fmt.Sprintf("from=%s&to=%s", end.AddDate(-5, 0, 1).Format("2006-01-02"), to)
	paths := []string{
		"/api/v1/overview?period=last_7d",
		"/api/v1/overview?period=last_month",
		"/api/v1/insights",
		"/api/v1/activity/daily?" + rng,
		"/api/v1/heart/daily?" + rng,
		"/api/v1/heart/events?" + rng,
		"/api/v1/sleep/nights?" + rng,
		"/api/v1/workouts?" + rng,
		"/api/v1/training/load?" + rng,
		"/api/v1/training/weekly?" + rng,
		"/api/v1/days/" + to,
		"/api/v1/coverage",
	}
	var out []p95
	for _, p := range paths {
		if err := getJSON(base+p, cookie, nil); err != nil { // warm-up
			return nil, err
		}
		var ms []float64
		for range n {
			start := time.Now()
			if err := getJSON(base+p, cookie, nil); err != nil {
				return nil, err
			}
			ms = append(ms, float64(time.Since(start).Microseconds())/1000)
		}
		slices.Sort(ms)
		v := ms[min(len(ms)-1, int(float64(len(ms))*0.95))]
		out = append(out, p95{p, v})
	}
	return out, nil
}

func getJSON(url string, cookie *http.Cookie, dst any) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s %s", url, resp.Status, b)
	}
	if dst == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}
