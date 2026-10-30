package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	socket := filepath.Join(shortDir(t), "d.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return socket
}

func TestClientSendsTheAPIVersion(t *testing.T) {
	seen := make(chan string, 4)
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.Path + " " + r.Header.Get(api.Header)
		if r.URL.Path == "/api/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": connected\n\n")
			return
		}
		fmt.Fprint(w, "{}")
	})
	c := New(socket)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "/api/health "+api.Version {
		t.Fatalf("ping sent %q", got)
	}
	if _, err := c.Rows(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != api.Rows+" "+api.Version {
		t.Fatalf("rows sent %q", got)
	}
	ch, err := c.Events(context.Background(), "test", api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "/api/events "+api.Version {
		t.Fatalf("events sent %q", got)
	}
	for range ch {
	}
}

func TestClientHintsTheFixOfARefusal(t *testing.T) {
	var fix = "launchctl kickstart -k gui/501/" + brand.Name
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUpgradeRequired)
		fmt.Fprintf(w, `{"error": "this client speaks API 1.0 and the daemon 2.0; restart the older one", "fix": %q}`, fix)
	})
	c := New(socket)
	_, rowsErr := c.Rows(context.Background())
	_, eventsErr := c.Events(context.Background(), "test", api.Since{})
	for name, err := range map[string]error{"rows": rowsErr, "events": eventsErr} {
		var hinted interface{ Hint() string }
		if !errors.As(err, &hinted) || hinted.Hint() != fix || !strings.Contains(err.Error(), "the daemon 2.0") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestClientNamesADaemonThatIsDown(t *testing.T) {
	err := New(filepath.Join(shortDir(t), "nope.sock")).Ping(context.Background())
	if !errors.Is(err, ErrDown) {
		t.Fatalf("err = %v", err)
	}
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" {
		t.Fatalf("the dial error is lost: %v", err)
	}
}

func TestClientEventsDeliversChanges(t *testing.T) {
	socket := filepath.Join(shortDir(t), "d.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("client") != "app" || q.Get("since") != "e1:4" {
			t.Errorf("query = %v", q)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": connected\n\nid: e1:5\ndata: {\"type\":\"changed\",\"epoch\":\"e1\",\"rev\":5,\"units\":[{\"provider\":\"github\",\"key\":\"a/b#1\"}]}\n\n")
		fmt.Fprint(w, "id: e1:6\ndata: {\"type\":\"changed\",\"epoch\":\"e1\",\"rev\":6,\"health\":true}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := New(socket).Events(ctx, "app", api.Since{Epoch: "e1", Rev: 4})
	if err != nil {
		t.Fatal(err)
	}
	var got api.Change
	deadline := time.After(2 * time.Second)
	for got.Rev < 6 {
		select {
		case c, ok := <-ch:
			if !ok {
				t.Fatal("closed before the changes")
			}
			got.Merge(c)
		case <-deadline:
			t.Fatalf("changes delivered up to %+v", got)
		}
	}
	want := api.Change{Type: "changed", Epoch: "e1", Rev: 6, Units: []unit.Unit{{Provider: unit.GitHub, Key: "a/b#1"}}, Health: true}
	if got.Epoch != want.Epoch || got.Rev != want.Rev || !got.Health || len(got.Units) != 1 || got.Units[0] != want.Units[0] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	cancel()
	for range ch {
	}
}

func TestClientEventsClosesASilentStream(t *testing.T) {
	defer func(d time.Duration) { eventsIdle = d }(eventsIdle)
	eventsIdle = 50 * time.Millisecond
	socket := filepath.Join(shortDir(t), "d.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": connected\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	ch, err := New(socket).Events(context.Background(), "test", api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("a change from a stream that sent none")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a silent stream stayed open")
	}
}

func TestProbePostsTheRequestAndReadsTheResult(t *testing.T) {
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var req api.ProbeRequest
		if r.Method != http.MethodPost || r.URL.Path != api.AdminProbe || json.NewDecoder(r.Body).Decode(&req) != nil || req != (api.ProbeRequest{WorkspaceID: "ws", Home: "/h", Model: "haiku"}) {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{"pr":"https://github.com/loop-e2e/probe/pull/1","turn":"abc","session":"s1"}`)
	})
	res, err := New(socket).Probe(context.Background(), api.ProbeRequest{WorkspaceID: "ws", Home: "/h", Model: "haiku"})
	if err != nil || res != (api.ProbeResult{PR: "https://github.com/loop-e2e/probe/pull/1", Turn: "abc", Session: "s1"}) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestIntakeCallsNameTheIntake(t *testing.T) {
	seen := make(chan string, 3)
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery
		fmt.Fprint(w, `{"seq":42,"state":"briefing","log":["a"],"log_len":3}`)
	})
	c := New(socket)
	in, err := c.Intake(context.Background(), 42, 2, 5*time.Second)
	if err != nil || in.Seq != 42 || in.LogLen != 3 || len(in.Log) != 1 {
		t.Fatalf("%+v %v", in, err)
	}
	if err := c.IntakeWithout(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if err := c.IntakeCancel(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GET /api/intakes/42?after=2&wait=5", "POST /api/intakes/42/without?", "POST /api/intakes/42/cancel?"} {
		if got := <-seen; got != want {
			t.Errorf("sent %q, want %q", got, want)
		}
	}
}

func TestRowsSinceAndActionsNameTheirUnit(t *testing.T) {
	seen := make(chan string, 2)
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method + " " + r.URL.EscapedPath() + "?" + r.URL.RawQuery
		if r.URL.Path == api.Rows {
			fmt.Fprint(w, `{"epoch":"e1","rev":9,"full":false,"rows":[],"removed":[{"provider":"github","key":"a/b#2"}]}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"output":["done"]}`)
	})
	c := New(socket)
	page, err := c.RowsSince(context.Background(), api.Since{Epoch: "e1", Rev: 7})
	if err != nil || page.Rev != 9 || page.Full || len(page.Removed) != 1 || page.Removed[0].Key != "a/b#2" {
		t.Fatalf("%+v %v", page, err)
	}
	out, err := c.Action(context.Background(), unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, "mute", api.ActionRequest{})
	if err != nil || len(out) != 1 {
		t.Fatalf("%v %v", out, err)
	}
	for _, want := range []string{"GET /api/rows?since=e1%3A7", "POST /api/units/github/acme%2Fapi%237/mute?"} {
		if got := <-seen; got != want {
			t.Errorf("sent %q, want %q", got, want)
		}
	}
}

func TestPlanAsksTheQueryAndActsWithTheRequest(t *testing.T) {
	seen := make(chan string, 4)
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var body api.PlanActRequest
		json.NewDecoder(r.Body).Decode(&body)
		seen <- r.Method + " " + r.URL.RequestURI() + " " + strings.Join(body.IDs, ",") + " " + body.Action
		json.NewEncoder(w).Encode(api.PlanResponse{Items: []api.PlanItem{{ID: "a"}}, Errors: []string{}})
	})
	c := New(socket)
	res, err := c.Plan(context.Background(), api.PlanQuery{Workflow: "w", Trigger: "sweep"})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := <-seen; got != "GET /api/plan?trigger=sweep&workflow=w  " {
		t.Fatalf("plan sent %q", got)
	}
	if _, err := c.ActOnPlan(context.Background(), api.PlanActRequest{IDs: []string{"a", "b"}, Action: "apply"}); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "POST /api/plan a,b apply" {
		t.Fatalf("act sent %q", got)
	}
}

func TestDigestAsksTheDay(t *testing.T) {
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.Digest{Day: r.URL.Path + "?" + r.URL.RawQuery, Tasks: []string{"t-1"}})
	})
	d, err := New(socket).Digest(context.Background(), "2026-10-08")
	if err != nil || d.Day != api.DigestPath+"?day=2026-10-08" || len(d.Tasks) != 1 {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestClientReadsTheRefusalCode(t *testing.T) {
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintf(w, `{"error": "It is yours already", "refusal": %q}`, api.BuiltinWhy("take_over"))
	})
	_, err := New(socket).Rows(context.Background())
	var refused interface{ Refusal() string }
	if !errors.As(err, &refused) || refused.Refusal() != api.BuiltinWhy("take_over") {
		t.Fatalf("err = %v", err)
	}
}

func TestViewsRouteServesTheCatalog(t *testing.T) {
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/views":
			fmt.Fprint(w, `{"views":[{"id":"inbox","label":"Inbox"}],"areas":["OSS"],"contexts":[],"verbs":[]}`)
		case "/api/tasks":
			fmt.Fprint(w, `{"epoch":"e","rev":2,"full":true,"tasks":[{"id":"t-1","title":"Call the bank","units":[],"items":[],"verbs":[],"contexts":[]}],"removed":[]}`)
		case "/api/tasks/t-1/done":
			fmt.Fprint(w, `{"task":{"id":"t-1","state":"done"}}`)
		default:
			http.NotFound(w, r)
		}
	})
	c := New(socket)
	ctx := context.Background()
	cat, err := c.Views(ctx)
	if err != nil || len(cat.Views) != 1 || cat.Views[0].Label != "Inbox" || cat.Areas[0] != "OSS" {
		t.Fatalf("views %+v %v", cat, err)
	}
	page, err := c.TasksSince(ctx, TaskQuery{View: "inbox"}, api.Since{})
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].Title != "Call the bank" {
		t.Fatalf("page %+v %v", page, err)
	}
	res, err := c.TaskDo(ctx, "t-1", "done", api.TaskRequest{Source: "cli"})
	if err != nil || res.Task.State != "done" {
		t.Fatalf("result %+v %v", res, err)
	}
}
