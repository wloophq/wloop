package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
)

func TestAPluginActionRelaysItsArgumentsAndTheAnswer(t *testing.T) {
	t.Parallel()
	var got []api.PluginActionRequest
	var ids []string
	s := newServerWith(t, func(d *Deps) {
		d.PluginAction = func(_ context.Context, id string, req api.PluginActionRequest) (json.RawMessage, error) {
			ids, got = append(ids, id), append(got, req)
			if req.Action == "eval" {
				return nil, errors.New("the plugin failed")
			}
			return json.RawMessage(`{"conversation":"C0000000001"}`), nil
		}
	})
	rec := post(t, s, api.PluginActionPath("notes", "send"), `{"args":{"target":"#general","dry_run":true}}`)
	if rec.Code != 200 || rec.Body.String() != `{"conversation":"C0000000001"}` {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	if len(got) != 1 || ids[0] != "notes" || got[0].Action != "send" || got[0].Actor != "person" || string(got[0].Args) != `{"target":"#general","dry_run":true}` {
		t.Fatalf("relayed %v %+v", ids, got)
	}
	if rec := post(t, s, api.PluginActionPath("notes", "eval"), `{"args":{}}`); rec.Code != 502 {
		t.Fatalf("a plugin's error: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, newServerWith(t, func(*Deps) {}), api.PluginActionPath("notes", "send"), `{}`); rec.Code != 404 {
		t.Fatalf("no plugin actions: %d", rec.Code)
	}
}

func TestAPluginActionNamesTheAgentForASessionsRequest(t *testing.T) {
	t.Parallel()
	var got []api.PluginActionRequest
	s := newServerWith(t, func(d *Deps) {
		d.PluginAction = func(_ context.Context, _ string, req api.PluginActionRequest) (json.RawMessage, error) {
			got = append(got, req)
			return json.RawMessage(`{}`), nil
		}
	})
	req := apiRequest("POST", api.PluginActionPath("notes", "send"), strings.NewReader(`{"args":{}}`))
	req.Header.Set(api.SessionHeader, "term_agent1")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || len(got) != 1 || got[0].Actor != "agent" {
		t.Fatalf("%d %s: relayed %+v, want the agent as its actor", rec.Code, rec.Body, got)
	}
}

// signalWriter closes seen once what it got holds want.
type signalWriter struct {
	mu   sync.Mutex
	got  strings.Builder
	want string
	seen chan struct{}
}

func (w *signalWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	had := strings.Contains(w.got.String(), w.want)
	w.got.Write(b)
	if !had && strings.Contains(w.got.String(), w.want) {
		close(w.seen)
	}
	return len(b), nil
}

func TestAPluginsCommandLineStreamsWhatItWritesBeforeItEnds(t *testing.T) {
	t.Parallel()
	out := &signalWriter{want: "scored 1 of 40\n", seen: make(chan struct{})}
	var args []string
	s := newServerWith(t, func(d *Deps) {
		d.PluginRun = func(ctx context.Context, id string, a []string, stdout, stderr io.Writer) (int, error) {
			args = append([]string{id}, a...)
			fmt.Fprint(stdout, "scored 1 of 40\n")
			select {
			case <-out.seen:
			case <-time.After(10 * time.Second):
				return 0, errors.New("the client never got the first line before the command ended")
			}
			fmt.Fprint(stderr, "spent $0.02\n")
			fmt.Fprint(stdout, "scored 40 of 40\n")
			return 3, nil
		}
	})
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(s.Handler())
	hs.Listener = l
	hs.Start()
	t.Cleanup(hs.Close)

	var errOut strings.Builder
	exit, err := client.New(sock).PluginRun(context.Background(), "notes", []string{"eval", "run"}, out, &errOut)
	if err != nil || exit != 3 {
		t.Fatalf("exit %d, %v", exit, err)
	}
	if got := out.got.String(); got != "scored 1 of 40\nscored 40 of 40\n" || errOut.String() != "spent $0.02\n" {
		t.Fatalf("stdout %q, stderr %q", got, errOut.String())
	}
	if !slices.Equal(args, []string{"notes", "eval", "run"}) {
		t.Fatalf("ran %v", args)
	}
}

func TestAnAgentsSessionRunsNoPluginCommandLine(t *testing.T) {
	t.Parallel()
	ran := false
	s := newServerWith(t, func(d *Deps) {
		d.PluginRun = func(context.Context, string, []string, io.Writer, io.Writer) (int, error) {
			ran = true
			return 0, nil
		}
	})
	req := apiRequest("POST", api.PluginRunPath("notes"), strings.NewReader(`{"args":["eval"]}`))
	req.Header.Set(api.SessionHeader, "term_agent1")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 || ran {
		t.Fatalf("%d %s, ran %v", rec.Code, rec.Body, ran)
	}
}
