package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
)

func TestStoreWaitAnswersItsHealthAndRefusesTheRestUntilReady(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "sw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	var ready atomic.Bool
	st := api.StoreStatus{Version: 3, Reads: 1, State: api.StoreTooNew, Message: "the store is newer", Fix: "fix-it restore"}
	w := StoreWait{
		Version: "v0.2.0", StateDir: dir, Socket: filepath.Join(dir, "d.sock"),
		LockPath: filepath.Join(dir, "daemon.lock"), InfoPath: filepath.Join(dir, "http.json"),
		Status: func() api.StoreStatus { return st },
		Ready:  ready.Load,
		Every:  10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done, exited := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(exited)
		done <- w.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-exited
	})
	hc := &http.Client{Transport: client.UnixTransport(w.Socket), Timeout: 2 * time.Second}
	var resp *http.Response
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if resp, err = hc.Get("http://daemon/api/health"); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	var h api.Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if h.Version != "v0.2.0" || h.Store == nil || h.Store.State != api.StoreTooNew || len(h.Warnings) != 1 || h.Warnings[0] != "store: the store is newer; run `fix-it restore`" {
		t.Fatalf("health %+v", h)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://daemon/api/rows", nil)
	req.Header.Set(api.Header, api.Version)
	resp, err = hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var refusal map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&refusal)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || refusal["error"] != "the store is not open: the store is newer" || refusal["fix"] != "fix-it restore" {
		t.Fatalf("%d %v", resp.StatusCode, refusal)
	}
	if info, err := ReadInfo(w.InfoPath); err != nil || info.Port == 0 || len(info.Token) < minTokenLen {
		t.Fatalf("cockpit info %+v %v", info, err)
	}
	ready.Store(true)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StoreWait did not return once ready")
	}
	if _, err := os.Stat(w.Socket); !os.IsNotExist(err) {
		t.Fatalf("the socket stays after the wait: %v", err)
	}
}
