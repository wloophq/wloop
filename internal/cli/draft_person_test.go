package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/wloophq/wloop/internal/plugins/bundled/bundledtest"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// outboxDaemon serves te's drafts, published by pub, on a socket of its own; interactive says whether the CLI
// reads from a terminal.
func outboxDaemon(t *testing.T, te *testEnv, interactive bool, edits ...func(*daemon.Deps)) *sqlite.DB {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mods, err := buildSources([]compiledPlugin{publishing(&recordingPublisher{})}, te.cfg, moduleDeps{DB: db, Clock: clockFunc(te.Now)}, bundledUI)
	if err != nil {
		t.Fatal(err)
	}
	d := te.serveTasks(daemon.Deps{Cfg: te.cfg, Version: "test", Now: te.Now, Log: t.Logf, Intents: te.loadedIntents}, db, te.cfg, mods, t.Logf)
	d = te.serveOutbox(d, db, te.cfg, mods, t.Logf)
	d.Outbox.Kinds = bundledtest.Shapes{}
	for _, edit := range edits {
		edit(&d)
	}
	h := daemon.New(d).Handler()
	te.Socket = serveSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == api.QueueAllPath {
			_ = json.NewEncoder(w).Encode(draftQueue(t, h, r))
			return
		}
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			draftPosts.Lock()
			draftPosts.bodies = append(draftPosts.bodies, string(body))
			draftPosts.paths = append(draftPosts.paths, r.URL.Path)
			draftPosts.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		h.ServeHTTP(w, r)
	}))
	draftPosts.Lock()
	draftPosts.bodies, draftPosts.paths = nil, nil
	draftPosts.Unlock()
	saved := draftInteractive
	draftInteractive = func(*Env) bool { return interactive }
	t.Cleanup(func() { draftInteractive = saved })
	return db
}

// draftQueue stands in for the queue h's daemon serves to asked, whose tasks this env does not hold: a send-draft item per
// draft whose gate waits, a retry per failed delivery, each with the draft's gate and the draft as its subject.
func draftQueue(t *testing.T, h http.Handler, asked *http.Request) api.Queue {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, api.Drafts, nil)
	r.Header = asked.Header.Clone()
	h.ServeHTTP(rec, r)
	var ds api.DraftsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ds); err != nil {
		t.Errorf("the drafts: %v: %s", err, rec.Body)
	}
	q := api.Queue{View: "needs-you", Title: "Needs you", Empty: "Nothing needs you.", Items: []api.QueueItem{}}
	for _, d := range ds.Drafts {
		subject, _ := json.Marshal(d)
		g := &api.GateView{ID: d.Gate, Kind: "draft", Status: "open", Subject: subject,
			Intents: []api.IntentView{{ID: "approve", Label: "Approve"}, {ID: "discard", Label: "Discard"}}}
		it := api.QueueItem{Kind: "send-draft", Word: "draft", Since: d.CreatedAt, Task: api.TaskView{ID: "draft:" + strconv.FormatInt(d.ID, 10), Title: draftLabel(d)},
			Gate: g, Primary: api.QueueAction{Kind: "gate", ID: "approve", Label: "Send"}}
		switch {
		case d.Intents[string(gate.Approved)] != "":
		case d.State == string(draft.Failed) && d.Gate != 0:
			g.Status = "approved"
			it.Primary = api.QueueAction{Kind: "verb", ID: "retry", Task: it.Task.ID, Args: map[string]string{"rev": strconv.FormatInt(d.Rev, 10)}, Label: "Retry"}
		default:
			continue
		}
		q.Items = append(q.Items, it)
	}
	q.Total = len(q.Items)
	return q
}

// draftPosts are the bodies the CLI posted to the outbox daemon, and their paths.
var draftPosts struct {
	sync.Mutex
	bodies, paths []string
}

func postedPaths() []string {
	draftPosts.Lock()
	defer draftPosts.Unlock()
	return slices.Clone(draftPosts.paths)
}

func posted() []string {
	draftPosts.Lock()
	defer draftPosts.Unlock()
	return slices.Clone(draftPosts.bodies)
}

func (te *testEnv) draftState(t *testing.T, db *sqlite.DB, id draft.ID) draft.Draft {
	t.Helper()
	d, err := db.Drafts().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestNeedsApproveConfirmsADraft(t *testing.T) {
	text := "First line.\nSecond line names @{github:ana} and @bob.\nThird line."
	seed := func(t *testing.T, interactive bool) (*testEnv, *sqlite.DB, draft.Draft) {
		te := newTestEnv(t, &fake.Manager{})
		db := outboxDaemon(t, te, interactive)
		d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Target: "#general", Text: text, State: draft.Held})
		return te, db, d
	}
	t.Run("not a terminal", func(t *testing.T) {
		for _, args := range [][]string{{"needs", "approve", "1"}, {"needs", "approve", "1", "--yes"}} {
			te, db, d := seed(t, false)
			if code := te.run(args...); code == 0 || !strings.Contains(te.err.String(), "cockpit") {
				t.Fatalf("%v: exit %d: %s", args, code, te.err)
			}
			if got := te.draftState(t, db, d.ID); got.State != draft.Held {
				t.Fatalf("%v approved it", args)
			}
		}
	})
	t.Run("an agent's shell", func(t *testing.T) {
		te, db, d := seed(t, true)
		te.Getenv = func(k string) string {
			if k == "CLAUDE_CODE_SESSION_ID" {
				return "session-1"
			}
			return ""
		}
		for _, args := range [][]string{{"needs", "approve", "1", "--yes"}, {"draft", "edit", "1", "agent words", "--rev", "0"}, {"draft", "discard", "1"}} {
			if code := te.run(args...); code == 0 || !strings.Contains(te.err.String(), "agent session") {
				t.Fatalf("%v: exit %d: %s", args, code, te.err)
			}
		}
		if got := te.draftState(t, db, d.ID); got.State != draft.Held || got.Text != d.Text {
			t.Fatal("an agent's shell changed it")
		}
	})
	t.Run("a payload without the rev seen, or not JSON", func(t *testing.T) {
		for _, args := range [][]string{{"needs", "approve", "1", "--payload", `{"event":"COMMENT"}`, "--yes"}, {"needs", "approve", "1", "--payload", "COMMENT", "--rev", "1", "--yes"}} {
			te, db, d := seed(t, true)
			if code := te.run(args...); code == 0 || !strings.Contains(te.err.String(), "--payload") {
				t.Fatalf("%v: exit %d: %s", args, code, te.err)
			}
			if got := te.draftState(t, db, d.ID); got.State != draft.Held {
				t.Fatalf("%v approved it", args)
			}
		}
	})
	t.Run("answered no", func(t *testing.T) {
		te, db, d := seed(t, true)
		te.In = strings.NewReader("n\n")
		if code := te.run("needs", "approve", "1"); code != 1 || !strings.Contains(te.out.String(), "Not approved") {
			t.Fatalf("exit %d: %s %s", code, te.out, te.err)
		}
		if got := te.draftState(t, db, d.ID); got.State != draft.Held {
			t.Fatal("approved on no")
		}
	})
	t.Run("answered yes", func(t *testing.T) {
		te, db, d := seed(t, true)
		te.In = strings.NewReader("y\n")
		if code := te.run("needs", "approve", "1"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		out := te.out.String()
		for _, want := range []string{"target:   #general", "to:       #general (C0GEN)", "  First line.", "  Second line names @{github:ana} and @bob.", "  Third line.", "mentions: @{github:ana}, @bob", "draft 1 queued"} {
			if !strings.Contains(out, want) {
				t.Errorf("printed no %q:\n%s", want, out)
			}
		}
		if got := te.draftState(t, db, d.ID); got.State != draft.Queued || got.Text != text || got.Edited {
			t.Fatalf("approved %+v", got)
		}
		if bodies := posted(); len(bodies) != 1 || !strings.Contains(bodies[0], `"rev":"1"`) {
			t.Fatalf("posted %v, want the rev shown", bodies)
		}
	})
	t.Run("yes with text", func(t *testing.T) {
		te, db, d := seed(t, true)
		if code := te.run("needs", "approve", "1", "--yes", "--text", "Only this.", "--rev", "1"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if !strings.Contains(te.out.String(), "  Only this.") || strings.Contains(te.out.String(), "Third line") {
			t.Fatalf("printed\n%s", te.out)
		}
		if got := te.draftState(t, db, d.ID); got.State != draft.Queued || got.Text != "Only this." || !got.Edited {
			t.Fatalf("approved %+v", got)
		}
	})
}

func TestDraftRetryNeedsConfirmAndATerminal(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "maybe out", State: draft.Failed, Error: "may have been posted: timeout", Cause: draft.CauseUnconfirmed})
	if code := te.run("draft", "retry", "1", "--yes"); code == 0 || !strings.Contains(te.err.String(), "confirm") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("draft", "retry", "1", "--yes", "--confirm"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.draftState(t, db, d.ID); got.State != draft.Queued {
		t.Fatalf("retried %+v", got)
	}
	if bodies := posted(); len(bodies) != 2 || !strings.Contains(bodies[1], `"rev":"1"`) {
		t.Fatalf("posted %v, want the rev shown", bodies)
	}
}

func TestDraftEditAndDiscard(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, false)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "draft", State: draft.Held})
	if code := te.run("draft", "edit", "1", "better text", "--rev", "1"); code != 0 {
		t.Fatalf("edit: exit %d: %s", code, te.err)
	}
	if got := te.draftState(t, db, d.ID); got.Text != "better text" || got.State != draft.Held {
		t.Fatalf("edited %+v", got)
	}
	if code := te.run("draft", "discard", "1", "--reason", "stale"); code != 0 {
		t.Fatalf("discard: exit %d: %s", code, te.err)
	}
	if got := te.draftState(t, db, d.ID); got.State != draft.Dropped || got.Reason != "user" {
		t.Fatalf("discarded %+v", got)
	}
	if code := te.run("draft", "frob"); code != 2 {
		t.Fatalf("unknown subcommand: exit %d", code)
	}
}

// editingReader edits the draft while the CLI waits for the answer, then answers y.
type editingReader struct {
	edit func()
	done bool
}

func (r *editingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.edit()
	return copy(p, "y\n"), nil
}

func TestNeedsApproveRefusesAnEditMadeWhileAsking(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "as shown", State: draft.Held})
	te.In = &editingReader{edit: func() {
		d.Text = "swapped while asking"
		if _, err := db.Drafts().Update(context.Background(), d, draft.Held); err != nil {
			t.Error(err)
		}
	}}
	if code := te.run("needs", "approve", "1"); code == 0 || !strings.Contains(te.err.String(), "changed since you saw it") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.draftState(t, db, d.ID); got.State != draft.Held {
		t.Fatalf("approved %+v", got)
	}
}

func TestNeedsShowSaysWhenTheDestinationIsUnknown(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, false)
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "hi", State: draft.Held})
	if code := te.run("needs", "show", "1"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "To          destination unknown") {
		t.Fatalf("printed\n%s", te.out)
	}
}

// knowThreads makes the thread provider one the CLI names units of, and waits little for a post.
func knowThreads(t *testing.T) {
	t.Helper()
	saved := plugins
	plugins = append(slices.Clone(plugins), threadModule(threadUnit))
	t.Cleanup(func() { plugins = saved })
	wait := postWait
	postWait = 10 * time.Millisecond
	t.Cleanup(func() { postWait = wait })
}

func TestDraftNewSavesOrSends(t *testing.T) {
	target := "thread:" + threadUnit.Key
	t.Run("saved", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		db := outboxDaemon(t, te, false)
		knowThreads(t)
		if code := te.run("draft", "new", target, "Friday works"); code != 0 || !strings.Contains(te.out.String(), "draft 1 saved for your approval") {
			t.Fatalf("exit %d: %s %s", code, te.out, te.err)
		}
		if got := te.draftState(t, db, 1); got.State != draft.Held || got.Kind != draft.Kind("reply") || got.Text != "Friday works" || got.Origin.Kind != draft.OriginUser || got.Unit != threadUnit {
			t.Fatalf("draft %+v", got)
		}
		if code := te.run("draft", "new", target, "--react", ":eyes:"); code != 0 {
			t.Fatalf("react: exit %d: %s", code, te.err)
		}
		if got := te.draftState(t, db, 2); got.Kind != draft.Kind("react") || got.Reaction != "eyes" {
			t.Fatalf("reaction %+v", got)
		}
	})
	t.Run("sent with --yes", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		db := outboxDaemon(t, te, false)
		knowThreads(t)
		if code := te.run("draft", "new", target, "Shipped", "--send", "--yes"); code != 0 || !strings.Contains(te.out.String(), "draft 1 queued") {
			t.Fatalf("exit %d: %s %s", code, te.out, te.err)
		}
		if got := te.draftState(t, db, 1); got.State != draft.Queued {
			t.Fatalf("draft %+v", got)
		}
	})
	t.Run("--send asks at a terminal", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		outboxDaemon(t, te, false)
		knowThreads(t)
		if code := te.run("draft", "new", target, "Shipped", "--send"); code == 0 || !strings.Contains(te.err.String(), "standard input is not one") {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		te = newTestEnv(t, &fake.Manager{})
		db := outboxDaemon(t, te, true)
		knowThreads(t)
		te.In = strings.NewReader("n\n")
		if code := te.run("draft", "new", target, "Shipped", "--send"); code != 1 || !strings.Contains(te.out.String(), "Not sent; draft 1 waits for your approval") {
			t.Fatalf("exit %d: %s %s", code, te.out, te.err)
		}
		if got := te.draftState(t, db, 1); got.State != draft.Held {
			t.Fatalf("draft %+v", got)
		}
		te.In = strings.NewReader("y\n")
		if code := te.run("draft", "new", target, "Shipped", "--send"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if got := te.draftState(t, db, 2); got.State != draft.Queued {
			t.Fatalf("draft %+v", got)
		}
	})
	t.Run("refused", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		outboxDaemon(t, te, true)
		knowThreads(t)
		for args, want := range map[string]string{
			"nowhere:x|hi":       "names no unit",
			target + "|hi|--yes": "--yes goes with --send",
			target:               "a unit and the reply's markdown",
		} {
			te.err.Reset()
			if code := te.run(append([]string{"draft", "new"}, strings.Split(args, "|")...)...); code == 0 || !strings.Contains(te.err.String(), want) {
				t.Errorf("%s: exit %d: %s", args, code, te.err)
			}
		}
	})
}

func TestTheCLIDecidesDraftsThroughTheirGates(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	ctx := context.Background()
	approve := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "approve me", State: draft.Held})
	drop := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "drop me", State: draft.Held})
	failed := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "failed once", State: draft.Failed, Error: "refused"})
	gateOf := func(id draft.ID) gate.Gate {
		t.Helper()
		d := te.draftState(t, db, id)
		g, ok, err := te.outbox.GateOf(ctx, d)
		if err != nil || !ok {
			t.Fatalf("draft %d has no gate: %v", id, err)
		}
		return g
	}
	ga, gd := gateOf(approve.ID), gateOf(drop.ID)

	if code := te.run("needs", "approve", strconv.FormatInt(ga.ID, 10), "--yes"); code != 0 {
		t.Fatalf("approve: exit %d: %s", code, te.err)
	}
	if code := te.run("draft", "discard", "2"); code != 0 {
		t.Fatalf("discard: exit %d: %s", code, te.err)
	}
	want := []string{fmt.Sprintf("/api/gates/%d/approve", ga.ID), fmt.Sprintf("/api/gates/%d/discard", gd.ID)}
	if got := postedPaths(); !slices.Equal(got, want) {
		t.Fatalf("the CLI posted to %v, want the drafts' gates %v", got, want)
	}
	if g := gateOf(approve.ID); g.Status != gate.Approved || g.By != task.ActorPerson || te.draftState(t, db, approve.ID).State != draft.Queued {
		t.Fatalf("approved gate %+v", g)
	}
	if g := gateOf(drop.ID); g.Status != gate.Rejected || g.By != task.ActorPerson || te.draftState(t, db, drop.ID).State != draft.Dropped {
		t.Fatalf("discarded gate %+v", g)
	}

	te.out.Reset()
	if code := te.run("needs"); code != 0 {
		t.Fatalf("list: exit %d: %s", code, te.err)
	}
	ref := strconv.FormatInt(gateOf(failed.ID).ID, 10)
	if lines := strings.Split(strings.TrimSpace(te.out.String()), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], ref+"\t") {
		t.Fatalf("needs lists %q, want the failed draft its gate approved alone", te.out)
	}
	te.out.Reset()
	if code := te.run("needs", "show", ref); code != 0 || !strings.Contains(te.out.String(), "draft retry 3") {
		t.Fatalf("show: exit %d: %s", code, te.out)
	}
	if code := te.run("needs", "approve", ref, "--yes"); code != 0 || te.draftState(t, db, failed.ID).State != draft.Queued {
		t.Fatalf("approving a failed delivery retries it: exit %d: %s", code, te.err)
	}
}

func TestAnAgentSessionsShellResolvesNoGate(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	held := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "drop me", State: draft.Held})
	te.Harness.SessionEnvs["fake"] = fake.SessionEnv{}
	te.Getenv = func(k string) string {
		if k == fake.HandleVar {
			return "term_1"
		}
		return ""
	}
	if code := te.run("draft", "discard", "1"); code == 0 {
		t.Fatalf("an agent's shell discarded a held draft: %s", te.out)
	}
	d := te.draftState(t, db, held.ID)
	if g, ok, err := te.outbox.GateOf(context.Background(), d); err != nil || !ok || g.Status != gate.Open || d.State != draft.Held {
		t.Fatalf("after an agent's discard: draft %+v, gate %+v %v", d, g, err)
	}
	te.Getenv = func(string) string { return "" }
	if code := te.run("draft", "discard", "1"); code != 0 || te.draftState(t, db, held.ID).State != draft.Dropped {
		t.Fatalf("a person's discard: exit %d: %s", code, te.err)
	}
}

func TestAnAgentShellApprovesThroughASessionTheUserDrives(t *testing.T) {
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, false, func(d *daemon.Deps) { d.Turns = te.Turns })
	te.Harness.SessionEnvs["fake"] = fake.SessionEnv{}
	worker, err := te.Turns.Resolve(ctx, "fake", "term_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := te.Turns.Typed(ctx, worker, "/review https://example.test/acme/api/pull/7"); err != nil {
		t.Fatal(err)
	}
	shell := func(handle string) {
		te.Getenv = func(k string) string {
			switch k {
			case fake.HandleVar:
				return handle
			case "CLAUDE_CODE_SESSION_ID":
				return "agent-" + handle
			}
			return ""
		}
	}
	held := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "on it", State: draft.Held})
	open, ok, err := te.outbox.GateOf(ctx, held)
	if err != nil || !ok {
		t.Fatalf("the draft's gate: %v", err)
	}
	ref := strconv.FormatInt(open.ID, 10)
	shell("term_1")
	if code := te.run("needs", "approve", ref, "--yes"); code == 0 || te.draftState(t, db, held.ID).State != draft.Held {
		t.Fatalf("a worker session's shell approved: exit %d: %s", code, te.err)
	}
	shell("term_2")
	if code := te.run("needs", "approve", ref, "--yes"); code != 0 {
		t.Fatalf("the shell of a session the user drives: exit %d: %s", code, te.err)
	}
	d := te.draftState(t, db, held.ID)
	g, ok, err := te.outbox.GateOf(ctx, d)
	if err != nil || !ok || g.Status != gate.Approved || g.By != task.ActorAgent || g.BySession == "" || g.BySession == worker {
		t.Fatalf("the gate %+v %v, want approved through the session the user drives", g, err)
	}
}

func TestDraftCommandsFindDraftVerbsByHandler(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{"tasks.toml": `[verbs.edit]
handler = "retry"

[verbs.retry]
handler = "edit"
`})
	db := outboxDaemon(t, te, true)
	failed := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "failed once", State: draft.Failed, Error: "refused"})
	if code := te.run("draft", "retry", "1", "--yes"); code != 0 {
		t.Fatalf("retry: exit %d: %s %v", code, te.err, postedPaths())
	}
	if got := postedPaths(); !slices.Equal(got, []string{"/api/tasks/draft:1/edit"}) {
		t.Fatalf("retry posted %v, want the verb whose handler is retry", got)
	}
	if got := te.draftState(t, db, failed.ID); got.State != draft.Queued {
		t.Fatalf("retried %+v", got)
	}
	held := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "draft", State: draft.Held})
	if code := te.run("draft", "edit", "2", "better", "--rev", "1"); code != 0 {
		t.Fatalf("edit: exit %d: %s", code, te.err)
	}
	if got := postedPaths(); len(got) != 2 || got[1] != "/api/tasks/draft:2/retry" {
		t.Fatalf("edit posted %v, want the verb whose handler is edit", got)
	}
	if got := te.draftState(t, db, held.ID); got.Text != "better" {
		t.Fatalf("edited %+v", got)
	}
}

// TestDraftEditAndApproveTextSendTheRevShown makes a person's change name the rev they read: without --rev it
// is a usage error, and an agent's revision after needs show gives a conflict and keeps the revision.
func TestDraftEditAndApproveTextSendTheRevShown(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "as shown", State: draft.Held})
	if code := te.run("needs", "show", "1"); code != 0 || !strings.Contains(te.out.String(), "Rev         1") {
		t.Fatalf("show: exit %d: %s", code, te.out)
	}
	for _, args := range [][]string{{"draft", "edit", "1", "mine"}, {"needs", "approve", "1", "--yes", "--text", "mine"}} {
		if code := te.run(args...); code != 2 || !strings.Contains(te.err.String(), "--rev") {
			t.Errorf("%v without --rev: exit %d: %s", args, code, te.err)
		}
	}
	d.Text = "the agent's revision"
	if _, err := db.Drafts().Update(context.Background(), d, draft.Held); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"draft", "edit", "1", "mine", "--rev", "1"}, {"needs", "approve", "1", "--yes", "--text", "mine", "--rev", "1"}} {
		if code := te.run(args...); code == 0 || !strings.Contains(te.err.String(), "changed since you saw it") {
			t.Errorf("%v after a revision: exit %d: %s", args, code, te.err)
		}
	}
	if got := te.draftState(t, db, d.ID); got.Text != "the agent's revision" || got.State != draft.Held {
		t.Fatalf("stored %+v, want the agent's revision held", got)
	}
}

// TestASessionShellsNewSendPostsAtOnce: draft new --send from the shell of a session the user drives writes the
// session's draft, never the person's, and approves it through the session at once; the command says so.
func TestASessionShellsNewSendPostsAtOnce(t *testing.T) {
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, false, func(d *daemon.Deps) { d.Turns = te.Turns })
	knowThreads(t)
	te.Harness.SessionEnvs["fake"] = fake.SessionEnv{}
	mine, err := te.Turns.Resolve(ctx, "fake", "term_2")
	if err != nil {
		t.Fatal(err)
	}
	te.Getenv = func(k string) string {
		switch k {
		case fake.HandleVar:
			return "term_2"
		case "CLAUDE_CODE_SESSION_ID":
			return "agent-term_2"
		}
		return ""
	}
	if code := te.run("draft", "new", "thread:"+threadUnit.Key, "Shipped", "--send"); code != 0 || !strings.Contains(te.out.String(), "queued") {
		t.Fatalf("exit %d: %s %s", code, te.out, te.err)
	}
	got := te.draftState(t, db, 1)
	if got.State != draft.Queued || got.Origin.Session != mine || got.By != (draft.Actor{Kind: draft.ActorSession, Session: mine}) {
		t.Fatalf("the session's draft %+v by %+v", got, got.By)
	}
}
