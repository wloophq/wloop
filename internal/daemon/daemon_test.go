package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
)

func testDeps(t *testing.T) Deps {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	stDB, err := sqlitetest.Open(filepath.Join(dir, "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	st := inventory.New(stDB)
	t.Cleanup(func() { stDB.Close() })
	cfg := config.Defaults()
	cfg.HTTP.Port = 0
	return withStore(Deps{
		Pauses:   sqlite.NewRounds(stDB),
		Activity: stDB,
		Cfg:      cfg,
		Now:      time.Now,
		Version:  "test",
		Log:      t.Logf,
		StateDir: dir,
		Socket:   filepath.Join(dir, "daemon.sock"),
		LockPath: filepath.Join(dir, "daemon.lock"),
		InfoPath: filepath.Join(dir, "http.json"),
	}, st)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runServer runs s until t ends; stop cancels the run and returns its error,
// once however often it is called.
func runServer(t *testing.T, s *Server) (stop func() error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	stop = sync.OnceValue(func() error {
		cancel()
		return <-done
	})
	t.Cleanup(func() { stop() })
	return stop
}

func newTestServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func socketClient(path string) *http.Client {
	return &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
}

func TestRunServesSocketAndGuardsTCP(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	stop := runServer(t, New(d))
	waitFor(t, func() bool { _, err := os.Stat(d.InfoPath); return err == nil })

	var info struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	b, err := os.ReadFile(d.InfoPath)
	if err != nil || json.Unmarshal(b, &info) != nil || info.Port == 0 || info.Token == "" {
		t.Fatalf("info %s: %v", b, err)
	}
	if fi, _ := os.Stat(d.InfoPath); fi.Mode().Perm() != 0o600 {
		t.Fatalf("info mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(d.Socket); fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", fi.Mode().Perm())
	}

	resp, err := socketClient(d.Socket).Get("http://daemon/api/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("socket health: %v %v", resp, err)
	}
	resp.Body.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d/api/health", info.Port)
	resp, err = http.Get(url)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tcp without token: %v %v", resp, err)
	}
	resp.Body.Close()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set(api.Header, api.Version)
	req.AddCookie(&http.Cookie{Name: tokenCookie, Value: info.Token})
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("tcp with token: %v %v", resp, err)
	}
	resp.Body.Close()

	if err := New(d).Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second daemon: %v", err)
	}

	if err := stop(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(d.Socket); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	stale, err := net.Listen("unix", d.Socket)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	stop := runServer(t, New(d))
	waitFor(t, func() bool { _, err := os.Stat(d.InfoPath); return err == nil })
	resp, err := socketClient(d.Socket).Get("http://daemon/api/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("socket health: %v %v", resp, err)
	}
	resp.Body.Close()
	if err := stop(); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestASocketPathTooLongSaysSo(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	d.Socket = filepath.Join(d.StateDir, strings.Repeat("s", 120)+".sock")
	err := New(d).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "too long for a unix socket") || !strings.Contains(err.Error(), d.Socket) {
		t.Fatalf("run: %v", err)
	}
}

func TestShutdownDoesNotWaitForeverOnAStuckJob(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{}, 1)
	d.Sweep = SweepFunc(func(context.Context) (string, string, error) {
		started <- struct{}{}
		<-release
		return "", "", nil
	})
	s := New(d)
	s.grace = 100 * time.Millisecond
	stop := runServer(t, s)
	<-started
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Run waited on a job that ignores its context")
	}
	if _, err := os.Stat(d.Socket); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestSweepWaitsForTheServerMutex(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d := testDeps(t)
		var runs atomic.Int32
		d.Sweep = SweepFunc(func(context.Context) (string, string, error) { runs.Add(1); return "", "", nil })
		s := New(d)
		s.deps.LoopMu.Lock()
		unlock := sync.OnceFunc(s.deps.LoopMu.Unlock)
		finished := make(chan struct{})
		t.Cleanup(func() {
			unlock()
			<-finished
		})
		go func() {
			s.jobs["sweep"].once(context.Background(), t.Logf, func(string, string) {})
			close(finished)
		}()
		synctest.Wait()
		if runs.Load() != 0 {
			t.Fatal("sweep ran while the server mutex was held")
		}
		unlock()
		<-finished
		if runs.Load() != 1 {
			t.Fatalf("runs %d after unlock", runs.Load())
		}
	})
}

func TestZeroIntervalsFallBackToDefaults(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	d.Cfg.Sweep.Fast, d.Cfg.Sweep.Slow = 0, 0
	d.Collector = &ghsource.Collector{}
	d.Sweep = SweepFunc(func(context.Context) (string, string, error) { return "", "", nil })
	s := New(d)
	def := config.Defaults().Sweep
	for name, want := range map[string]time.Duration{"fast": time.Duration(def.Fast), "slow": time.Duration(def.Slow), "sweep": time.Duration(def.Slow)} {
		if got := s.jobs[name].every; got != want {
			t.Fatalf("%s every %v, want %v", name, got, want)
		}
	}
}

func TestWriteInfoReplacesAtomically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "http.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeInfo(p, 1234, "tok"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{"port":1234,"token":"tok"}` {
		t.Fatalf("content %s", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestTokenQuerySetsCookieAndRedirects(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	h := s.AuthHandler()
	srv := newTestServer(t, h)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv + "/api/health?token=" + s.token)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/api/health" {
		t.Fatalf("status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "loop_token" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != s.token || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie %+v", cookie)
	}
	req, _ := http.NewRequest(http.MethodGet, srv+"/api/health", nil)
	req.Header.Set(api.Header, api.Version)
	req.AddCookie(cookie)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("with cookie: %v %v", resp, err)
	}
	resp.Body.Close()
}

func TestTokenRedirectStaysOnThisHost(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	srv := newTestServer(t, s.AuthHandler())
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"//evil.test/x", "/\\evil.test/x"} {
		resp, err := client.Get(srv + path + "?token=" + s.token)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Location"); got != "/evil.test/x" {
			t.Fatalf("%s: location %q", path, got)
		}
	}
}

func TestCrossSitePostIsForbidden(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	srv := newTestServer(t, s.AuthHandler())
	for site, want := range map[string]int{"cross-site": http.StatusForbidden, "same-site": http.StatusForbidden, "same-origin": http.StatusNotFound, "none": http.StatusNotFound} {
		req, _ := http.NewRequest(http.MethodPost, srv+"/api/jobs/none", nil)
		req.Header.Set(api.Header, api.Version)
		req.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})
		req.Header.Set("Sec-Fetch-Site", site)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s: status %d, want %d", site, resp.StatusCode, want)
		}
	}
}

func TestTheCockpitAnswersOnlyItsLoopbackHost(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	srv := newTestServer(t, s.AuthHandler())
	port := strings.TrimPrefix(srv, "http://127.0.0.1:")
	for host, want := range map[string]int{
		"127.0.0.1:" + port:        http.StatusOK,
		"localhost:" + port:        http.StatusOK,
		"127.0.0.1:1":              http.StatusMisdirectedRequest,
		"evil.example:" + port:     http.StatusMisdirectedRequest,
		"127.0.0.1.nip.io:" + port: http.StatusMisdirectedRequest,
		"127.0.0.1":                http.StatusMisdirectedRequest,
	} {
		req, _ := http.NewRequest(http.MethodGet, srv+"/api/health", nil)
		req.Host = host
		req.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %s: status %d, want %d", host, resp.StatusCode, want)
		}
	}
}

func TestAWriteWithoutTheAPIHeaderIsForbidden(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	srv := newTestServer(t, s.AuthHandler())
	for name, c := range map[string]struct {
		header, query string
		want          int
	}{
		"no Sec-Fetch-Site, no header": {"", "", http.StatusForbidden},
		"the api query instead":        {"", "?api=" + api.Version, http.StatusForbidden},
		"the header":                   {api.Version, "", http.StatusNotFound},
	} {
		req, _ := http.NewRequest(http.MethodPost, srv+"/api/jobs/none"+c.query, nil)
		if c.header != "" {
			req.Header.Set(api.Header, c.header)
		}
		req.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d", name, resp.StatusCode, c.want)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/none?api="+api.Version, nil))
	if rec.Code != http.StatusUpgradeRequired {
		t.Errorf("a write versioned by its query: %d", rec.Code)
	}
}

func TestCrossSiteWriteMethodsAreForbidden(t *testing.T) {
	t.Parallel()
	s := New(testDeps(t))
	srv := newTestServer(t, s.AuthHandler())
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(method, srv+"/api/jobs/none", nil)
		req.Header.Set(api.Header, api.Version)
		req.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status %d", method, resp.StatusCode)
		}
	}
}

func TestRunJobUnknownName(t *testing.T) {
	t.Parallel()
	if New(testDeps(t)).RunJob("nope") {
		t.Fatal("unknown job reported as triggered")
	}
}

func readInfoToken(t *testing.T, path string) string {
	t.Helper()
	var info struct {
		Token string `json:"token"`
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &info) != nil {
		t.Fatalf("info %s: %v", b, err)
	}
	return info.Token
}

func runUntilServing(t *testing.T, d Deps) (token string, stop func()) {
	t.Helper()
	run := runServer(t, New(d))
	waitFor(t, func() bool {
		resp, err := socketClient(d.Socket).Get("http://daemon/api/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	})
	return readInfoToken(t, d.InfoPath), func() {
		if err := run(); err != nil {
			t.Fatalf("run: %v", err)
		}
	}
}

func TestTokenSurvivesARestart(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	first, stop := runUntilServing(t, d)
	stop()
	if got := readInfoToken(t, d.InfoPath); got != first {
		t.Fatalf("info after stop has token %q, want %q", got, first)
	}
	second, stop := runUntilServing(t, d)
	defer stop()
	if second != first {
		t.Fatalf("token changed across a restart: %q then %q", first, second)
	}
}

func TestTokenIsNotReusedFromAnUnsafeInfoFile(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		body string
		mode os.FileMode
	}{
		"readable by others": {`{"port":1,"token":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}`, 0o644},
		"short token":        {`{"port":1,"token":"tok"}`, 0o600},
		"not json":           {`garbage`, 0o600},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := testDeps(t)
			if err := os.WriteFile(d.InfoPath, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(d.InfoPath, tc.mode); err != nil {
				t.Fatal(err)
			}
			token, stop := runUntilServing(t, d)
			defer stop()
			if token == "" || token == "ABCDEFGHIJKLMNOPQRSTUVWXYZ" || token == "tok" {
				t.Fatalf("reused token %q", token)
			}
		})
	}
}

func infoPort(t *testing.T, path string) int {
	t.Helper()
	var info struct {
		Port int `json:"port"`
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &info) != nil {
		t.Fatalf("info %s: %v", b, err)
	}
	return info.Port
}

func TestPortSurvivesARestart(t *testing.T) {
	d := testDeps(t)
	_, stop := runUntilServing(t, d)
	first := infoPort(t, d.InfoPath)
	stop()
	_, stop = runUntilServing(t, d)
	defer stop()
	if got := infoPort(t, d.InfoPath); got != first {
		t.Fatalf("port changed across a restart: %d then %d", first, got)
	}
}

func TestBusyPreviousPortFallsBackToAFreeOne(t *testing.T) {
	t.Parallel()
	d := testDeps(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	if err := writeInfo(d.InfoPath, port, "ABCDEFGHIJKLMNOPQRSTUVWXYZ"); err != nil {
		t.Fatal(err)
	}
	_, stop := runUntilServing(t, d)
	defer stop()
	if got := infoPort(t, d.InfoPath); got == port || got == 0 {
		t.Fatalf("port %d with %d busy", got, port)
	}
}

func socketHealth(t *testing.T, path string) api.Health {
	t.Helper()
	resp, err := socketClient(path).Get("http://daemon/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var h api.Health
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&h) != nil {
		t.Fatalf("health: %d", resp.StatusCode)
	}
	return h
}

func runUntilInfo(t *testing.T, d Deps) {
	t.Helper()
	stop := runServer(t, New(d))
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("run: %v", err)
		}
	})
	waitFor(t, func() bool { _, err := os.Stat(d.InfoPath); return err == nil })
}

func TestTakenMCPPortRefusesTheStart(t *testing.T) {
	t.Parallel()
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	d := testDeps(t)
	d.MCPPort = taken.Addr().(*net.TCPAddr).Port
	d.MCP = http.NotFoundHandler()
	err = New(d).Run(context.Background())
	if err == nil {
		t.Fatal("started without its MCP port")
	}
	for _, want := range []string{fmt.Sprintf("127.0.0.1:%d is taken", d.MCPPort), "mcp.port", brand.Command + " install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if _, err := os.Stat(d.Socket); !os.IsNotExist(err) {
		t.Errorf("the socket stayed: %v", err)
	}
	if _, err := os.Stat(d.InfoPath); !os.IsNotExist(err) {
		t.Errorf("the info file was written: %v", err)
	}
}

func TestRunServesMCP(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	d := testDeps(t)
	d.MCPPort = port
	d.MCP = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	runUntilInfo(t, d)

	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/mcp", port), "application/json", strings.NewReader("{}"))
	if err != nil || resp.StatusCode != http.StatusTeapot {
		t.Fatalf("%v %v", resp, err)
	}
	resp.Body.Close()
	if h := socketHealth(t, d.Socket); len(h.Warnings) != 0 {
		t.Fatalf("warnings %q", h.Warnings)
	}
}

func TestInfoIsWrittenAfterTheMCPListener(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	d := testDeps(t)
	d.MCPPort = port
	d.MCP = http.NotFoundHandler()
	var infoBefore atomic.Bool
	d.Log = func(format string, args ...any) {
		if strings.HasPrefix(format, "mcp on ") {
			if _, err := os.Stat(d.InfoPath); err == nil {
				infoBefore.Store(true)
			}
		}
		t.Logf(format, args...)
	}
	runUntilInfo(t, d)
	if infoBefore.Load() {
		t.Fatal("the info file was written before the MCP port listened")
	}
}

func TestUserAPIWaitsForTheReconcile(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	release := make(chan struct{})
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	d := testDeps(t)
	d.MCPPort = port
	d.MCP = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	d.Startup = func(context.Context) (api.ReconcileReport, error) {
		<-release
		return api.ReconcileReport{GoneSessions: []string{"term-9"}, At: at}, nil
	}
	runUntilInfo(t, d)

	req, _ := http.NewRequest(http.MethodPost, "http://daemon"+api.AgentSessionsStart, strings.NewReader("{}"))
	req.Header.Set(api.Header, api.Version)
	req.Header.Set(api.SessionHeader, "term-1")
	resp, err := socketClient(d.Socket).Do(req)
	if err != nil {
		t.Fatalf("AgentAPI waits for the reconcile: %v", err)
	}
	resp.Body.Close()
	resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%d/mcp", port), "application/json", strings.NewReader("{}"))
	if err != nil || resp.StatusCode != http.StatusTeapot {
		t.Fatalf("MCP waits for the reconcile: %v %v", resp, err)
	}
	resp.Body.Close()

	answered := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "http://daemon/api/rows", nil)
		req.Header.Set(api.Header, api.Version)
		resp, err := (&http.Client{Transport: socketClient(d.Socket).Transport}).Do(req)
		if err != nil {
			answered <- 0
			return
		}
		resp.Body.Close()
		answered <- resp.StatusCode
	}()
	select {
	case code := <-answered:
		t.Fatalf("UserAPI answered %d during the reconcile", code)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case code := <-answered:
		if code != http.StatusOK {
			t.Fatalf("held request: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held request was never answered")
	}
	req, _ = http.NewRequest(http.MethodGet, "http://daemon"+api.AdminDoctor, nil)
	req.Header.Set(api.Header, api.Version)
	resp, err = socketClient(d.Socket).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rep api.DoctorReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatal(err)
	}
	if rep.Reconcile == nil || !rep.Reconcile.At.Equal(at) || !slices.Equal(rep.Reconcile.GoneSessions, []string{"term-9"}) {
		t.Fatalf("reconcile %+v", rep.Reconcile)
	}
}

func TestUserAPIServesWhenTheReconcileRunsLong(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var sweeps atomic.Int32
	d := testDeps(t)
	d.Startup = func(context.Context) (api.ReconcileReport, error) {
		<-release
		return api.ReconcileReport{}, nil
	}
	d.Sweep = SweepFunc(func(context.Context) (string, string, error) {
		sweeps.Add(1)
		return "", "", nil
	})
	s := New(d)
	s.reconcile.wait = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	released := false
	t.Cleanup(func() {
		cancel()
		if !released {
			close(release)
		}
		<-done
	})
	waitFor(t, func() bool { _, err := os.Stat(d.InfoPath); return err == nil })
	req, _ := http.NewRequest(http.MethodGet, "http://daemon/api/rows", nil)
	req.Header.Set(api.Header, api.Version)
	resp, err := socketClient(d.Socket).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%v %v", resp, err)
	}
	resp.Body.Close()
	if h := socketHealth(t, d.Socket); !slices.Contains(h.Warnings, "reconcile still running") {
		t.Fatalf("warnings %q", h.Warnings)
	}
	close(release)
	released = true
	waitFor(t, func() bool { return sweeps.Load() > 0 })
}

func TestJobsWaitForTheStartupReconcile(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var sweeps atomic.Int32
		d := testDeps(t)
		d.Startup = func(context.Context) (api.ReconcileReport, error) {
			<-release
			return api.ReconcileReport{}, nil
		}
		d.Sweep = SweepFunc(func(context.Context) (string, string, error) {
			sweeps.Add(1)
			return "", "", nil
		})
		s := New(d)
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		t.Cleanup(func() {
			cancel()
			wg.Wait()
		})
		free := sync.OnceFunc(func() { close(release) })
		t.Cleanup(free)
		wg.Go(func() { s.runStartup(ctx) })
		s.startJobs(ctx, &wg)
		s.RunJob("sweep")
		synctest.Wait()
		if n := sweeps.Load(); n != 0 {
			t.Fatalf("%d sweeps ran beside the start-up reconcile", n)
		}
		free()
		synctest.Wait()
		if sweeps.Load() == 0 {
			t.Fatal("no sweep after the reconcile")
		}
	})
}

func TestRowFeedStartsWhenTheUserAPIOpens(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	d := testDeps(t)
	d.Changes = change.NewLog(change.DefaultWindow, nil)
	d.Startup = func(context.Context) (api.ReconcileReport, error) {
		<-release
		return api.ReconcileReport{}, nil
	}
	s := New(d)
	s.reconcile.wait = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		close(release)
		<-done
	})
	<-s.reconcile.ready
	waitFor(t, func() bool { return s.hub.position() != (api.Since{}) })
}

func TestAUserAPIReadOutlastsTheReconcileWait(t *testing.T) {
	t.Parallel()
	if client.ReadTimeout <= ReconcileWait {
		t.Fatalf("client.ReadTimeout %v does not outlast ReconcileWait %v", client.ReadTimeout, ReconcileWait)
	}
}

func TestAdminAPIIsSocketOnly(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.token = "a-token-long-enough-for-the-check"
	for _, op := range api.Operations {
		if !strings.HasPrefix(op.Path, api.AdminPrefix) {
			continue
		}
		rec := httptest.NewRecorder()
		req := apiRequest(op.Method, op.Path, strings.NewReader("{}"))
		req.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})
		s.AuthHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("the TCP port answered %d to %s", rec.Code, op.Pattern())
		}
	}
}

func TestDoctorRefusesAMalformedPullRequest(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", api.AdminDoctor+"?repo=acme/api&pr=nope", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
