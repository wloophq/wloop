package cli

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
)

// listenAsWindow subscribes as the window client and returns the requests
// relayed to it.
func listenAsWindow(t *testing.T, te *testEnv) <-chan api.WindowRequest {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	changes, err := client.New(te.Socket).Events(ctx, api.WindowClient, api.Since{})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	got := make(chan api.WindowRequest, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for c := range changes {
			for _, w := range c.Windows {
				got <- w
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return got
}

func nextWindowRequest(t *testing.T, got <-chan api.WindowRequest) api.WindowRequest {
	t.Helper()
	select {
	case w := <-got:
		return w
	case <-time.After(10 * time.Second):
		t.Fatal("no window request")
		return api.WindowRequest{}
	}
}

func TestOpenFocusesTheWindowWhenOneListens(t *testing.T) {
	t.Parallel()
	te, opened := cockpitEnv(t)
	got := listenAsWindow(t, te)
	if code := te.run("open", "--view", "needs-me"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if w := nextWindowRequest(t, got); w != (api.WindowRequest{Action: api.WindowOpen, Route: "view=needs-me"}) {
		t.Fatalf("requested %+v", w)
	}
	if len(*opened) != 0 {
		t.Fatalf("the browser opened %v", *opened)
	}
}

func TestOpenFallsBackToTheBrowserWhenNoWindowListens(t *testing.T) {
	t.Parallel()
	te, opened := cockpitEnv(t)
	if code := te.run("open", "--view", "needs-me"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if want := browserCommand() + " http://127.0.0.1:4321/?token=tok#view=needs-me"; !slices.Equal(*opened, []string{want}) {
		t.Fatalf("opened %v", *opened)
	}
}

func TestOpenRoutesToTheWindow(t *testing.T) {
	t.Parallel()
	for arg, route := range map[string]string{
		"":          "",
		"#task=t-1": "task=t-1",
		"http://127.0.0.1:4321/?token=tok#view=x": "view=x",
		"http://localhost:4321/#task=t-2":         "task=t-2",
	} {
		t.Run(arg, func(t *testing.T) {
			te, opened := cockpitEnv(t)
			got := listenAsWindow(t, te)
			args := []string{"open"}
			if arg != "" {
				args = append(args, arg)
			}
			if code := te.run(args...); code != 0 {
				t.Fatalf("exit %d: %s", code, te.err)
			}
			if w := nextWindowRequest(t, got); w != (api.WindowRequest{Action: api.WindowOpen, Route: route}) {
				t.Fatalf("requested %+v, want route %q", w, route)
			}
			if len(*opened) != 0 {
				t.Fatalf("the browser opened %v", *opened)
			}
		})
	}
}

func TestOpenACockpitRouteFallsBackToTheBrowserAtThatRoute(t *testing.T) {
	t.Parallel()
	te, opened := cockpitEnv(t)
	if code := te.run("open", "#task=t-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if want := browserCommand() + " http://127.0.0.1:4321/?token=tok#task=t-1"; !slices.Equal(*opened, []string{want}) {
		t.Fatalf("opened %v", *opened)
	}
}

func TestOpenPrintStillPrints(t *testing.T) {
	t.Parallel()
	te, _ := cockpitEnv(t)
	got := listenAsWindow(t, te)
	if code := te.run("open", "--print", "--view", "mine"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if want := "http://127.0.0.1:4321/?token=tok#view=mine\n"; te.out.String() != want {
		t.Fatalf("stdout %q", te.out)
	}
	select {
	case w := <-got:
		t.Fatalf("--print requested %+v", w)
	default:
	}
}
