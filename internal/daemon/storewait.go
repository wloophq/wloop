package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/web"
)

// StoreWait is the daemon while it cannot open its store: it answers its
// health with the store's state, refuses every other request with that
// state's message and fix, and returns once Ready says the store may open.
// It holds the daemon's lock and socket meanwhile, so clients reach it, not
// a second daemon.
type StoreWait struct {
	Version  string
	StateDir string
	Socket   string
	LockPath string
	InfoPath string
	// HTTPPort is the cockpit's configured port; 0 reuses the earlier one.
	HTTPPort int
	// Status is the store's state, read on each request.
	Status func() api.StoreStatus
	// Ready is asked every Every; true ends the wait.
	Ready func() bool
	Every time.Duration
}

func (w StoreWait) Run(ctx context.Context) error {
	if err := ensureStateDir(w.StateDir); err != nil {
		return err
	}
	unlock, err := LockFile(w.LockPath, ErrAlreadyRunning)
	if err != nil {
		return err
	}
	defer unlock()
	if limit := len(syscall.RawSockaddrUnix{}.Path); len(w.Socket) >= limit {
		return fmt.Errorf("socket path %s is %d bytes, too long for a unix socket (under %d); use a shorter state directory", w.Socket, len(w.Socket), limit)
	}
	if err := claimSocket(w.Socket); err != nil {
		return err
	}
	unixLn, err := net.Listen("unix", w.Socket)
	if err != nil {
		return err
	}
	defer os.Remove(w.Socket)
	if err := os.Chmod(w.Socket, 0o600); err != nil {
		unixLn.Close()
		return err
	}
	tcpLn, err := listenTCP(w.HTTPPort, w.InfoPath)
	if err != nil {
		unixLn.Close()
		return err
	}
	token := reusableToken(w.InfoPath)
	if token == "" {
		token = rand.Text()
	}
	if err := writeInfo(w.InfoPath, tcpLn.Addr().(*net.TCPAddr).Port, token); err != nil {
		unixLn.Close()
		tcpLn.Close()
		return err
	}
	h := w.handler()
	servers := []*http.Server{
		{Handler: h, ReadHeaderTimeout: 10 * time.Second},
		{Handler: authHandler(token, h), ReadHeaderTimeout: 10 * time.Second},
	}
	errc := make(chan error, len(servers))
	for i, ln := range []net.Listener{unixLn, tcpLn} {
		go func() {
			if err := servers[i].Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}
	t := time.NewTicker(w.Every)
	defer t.Stop()
	var serveErr error
wait:
	for {
		select {
		case <-ctx.Done():
			break wait
		case serveErr = <-errc:
			break wait
		case <-t.C:
			if w.Ready() {
				break wait
			}
		}
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownGrace)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(sctx)
	}
	return serveErr
}

func (w StoreWait) health() api.Health {
	st := w.Status()
	h := api.Health{Version: w.Version, Build: web.Build(w.Version), Jobs: []api.JobStatus{}, Warnings: []string{}, Store: &st}
	if st.Message != "" {
		h.Warnings = append(h.Warnings, StoreWarning(st))
	}
	return h
}

// StoreWarning is st as one line: the message and the command to run.
func StoreWarning(st api.StoreStatus) string {
	if st.Fix == "" {
		return "store: " + st.Message
	}
	return "store: " + st.Message + "; run `" + st.Fix + "`"
}

func (w StoreWait) handler() http.Handler {
	page := web.Handler(w.Version)
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
			writeJSON(rw, w.health())
		case strings.HasPrefix(r.URL.Path, "/api/"):
			st := w.Status()
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": "the store is not open: " + st.Message, "fix": st.Fix})
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			page.ServeHTTP(rw, r)
		default:
			http.Error(rw, "the store is not open", http.StatusServiceUnavailable)
		}
	})
}
