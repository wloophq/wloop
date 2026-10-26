package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// outboxDaemon serves te's drafts, published by pub, on a socket of its own; interactive says whether the CLI
// reads from a terminal.
func outboxDaemon(t *testing.T, te *testEnv, interactive bool) *sqlite.DB {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mods, err := buildSources([]source.Plugin{publishing(&recordingPublisher{})}, te.cfg, source.Deps{DB: db, Clock: clockFunc(te.Now)})
	if err != nil {
		t.Fatal(err)
	}
	d := te.serveOutbox(daemon.Deps{Cfg: te.cfg, Version: "test", Now: te.Now, Log: t.Logf}, db, te.cfg, mods, t.Logf)
	h := daemon.New(d).Handler()
	te.Socket = serveSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			draftPosts.Lock()
			draftPosts.bodies = append(draftPosts.bodies, string(body))
			draftPosts.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		h.ServeHTTP(w, r)
	}))
	draftPosts.Lock()
	draftPosts.bodies = nil
	draftPosts.Unlock()
	saved := outboxInteractive
	outboxInteractive = func(*Env) bool { return interactive }
	t.Cleanup(func() { outboxInteractive = saved })
	return db
}

// draftPosts are the bodies the CLI posted to the outbox daemon.
var draftPosts struct {
	sync.Mutex
	bodies []string
}

func posted() []string {
	draftPosts.Lock()
	defer draftPosts.Unlock()
	return slices.Clone(draftPosts.bodies)
}

func TestOutboxListGolden(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, false)
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "Thanks, merging now.\nSecond line stays out.", State: draft.Proposed,
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review"}})
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Kind: draft.Nudge, Target: "#general", Text: "Any news on this?", State: draft.Failed,
		Error: "may have been posted: timeout", Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "stalled-nudge"},
		CreatedAt: testNow.Add(time.Minute), UpdatedAt: testNow.Add(time.Minute)})
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "Closing this.", State: draft.Proposed,
		Approval: draft.Approval{Mode: draft.Window, Window: 10 * time.Minute}, Deadline: testNow.Add(10 * time.Minute),
		CreatedAt: testNow.Add(2 * time.Minute), UpdatedAt: testNow.Add(2 * time.Minute)})
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Kind: draft.Close, Text: "Left sending by a crash.", State: draft.Sending, SendingAt: testNow,
		CreatedAt: testNow.Add(3 * time.Minute), UpdatedAt: testNow.Add(3 * time.Minute)})
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "already out", State: draft.Sent})
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "only watched", State: draft.Discarded, Reason: "observe"})
	if code := te.run("outbox"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	golden := filepath.Join("testdata", "outbox_list.golden")
	if *update {
		if err := os.WriteFile(golden, te.out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if te.out.String() != string(want) {
		t.Fatalf("outbox printed\n%s\nwant\n%s", te.out, want)
	}
}

func (te *testEnv) draftState(t *testing.T, db *sqlite.DB, id draft.ID) draft.Draft {
	t.Helper()
	d, err := db.Drafts().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestOutboxApproveConfirms(t *testing.T) {
	text := "First line.\nSecond line names @{github:ana} and @bob.\nThird line."
	seed := func(t *testing.T, interactive bool) (*testEnv, *sqlite.DB, draft.Draft) {
		te := newTestEnv(t, &fake.Manager{})
		db := outboxDaemon(t, te, interactive)
		d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Target: "#general", Text: text, State: draft.Proposed})
		return te, db, d
	}
	t.Run("not a terminal", func(t *testing.T) {
		for _, args := range [][]string{{"outbox", "approve", "1"}, {"outbox", "approve", "1", "--yes"}} {
			te, db, d := seed(t, false)
			if code := te.run(args...); code == 0 || !strings.Contains(te.err.String(), "cockpit") {
				t.Fatalf("%v: exit %d: %s", args, code, te.err)
			}
			if got := te.draftState(t, db, d.ID); got.State != draft.Proposed {
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
		if code := te.run("outbox", "approve", "1", "--yes"); code == 0 || !strings.Contains(te.err.String(), "agent session") {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if got := te.draftState(t, db, d.ID); got.State != draft.Proposed {
			t.Fatal("an agent approved it")
		}
	})
	t.Run("answered no", func(t *testing.T) {
		te, db, d := seed(t, true)
		te.In = strings.NewReader("n\n")
		if code := te.run("outbox", "approve", "1"); code != 1 || !strings.Contains(te.out.String(), "Not approved") {
			t.Fatalf("exit %d: %s %s", code, te.out, te.err)
		}
		if got := te.draftState(t, db, d.ID); got.State != draft.Proposed {
			t.Fatal("approved on no")
		}
	})
	t.Run("answered yes", func(t *testing.T) {
		te, db, d := seed(t, true)
		te.In = strings.NewReader("y\n")
		if code := te.run("outbox", "approve", "1"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		out := te.out.String()
		for _, want := range []string{"target:   #general", "to:       #general (C0GEN)", "  First line.", "  Second line names @{github:ana} and @bob.", "  Third line.", "mentions: @{github:ana}, @bob", "draft 1 approved"} {
			if !strings.Contains(out, want) {
				t.Errorf("printed no %q:\n%s", want, out)
			}
		}
		if got := te.draftState(t, db, d.ID); got.State != draft.Approved || got.Text != text || got.Edited {
			t.Fatalf("approved %+v", got)
		}
		if bodies := posted(); len(bodies) != 1 || !strings.Contains(bodies[0], `"rev":0`) {
			t.Fatalf("posted %v, want the rev shown", bodies)
		}
	})
	t.Run("yes with text", func(t *testing.T) {
		te, db, d := seed(t, true)
		if code := te.run("outbox", "approve", "1", "--yes", "--text", "Only this."); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if !strings.Contains(te.out.String(), "  Only this.") || strings.Contains(te.out.String(), "Third line") {
			t.Fatalf("printed\n%s", te.out)
		}
		if got := te.draftState(t, db, d.ID); got.State != draft.Approved || got.Text != "Only this." || !got.Edited {
			t.Fatalf("approved %+v", got)
		}
	})
}

func TestOutboxRetryNeedsConfirmAndATerminal(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "maybe out", State: draft.Failed, Error: "may have been posted: timeout"})
	if code := te.run("outbox", "retry", "1", "--yes"); code == 0 || !strings.Contains(te.err.String(), "confirm") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("outbox", "retry", "1", "--yes", "--confirm"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.draftState(t, db, d.ID); got.State != draft.Approved {
		t.Fatalf("retried %+v", got)
	}
	if bodies := posted(); len(bodies) != 2 || !strings.Contains(bodies[1], `"rev":0`) {
		t.Fatalf("posted %v, want the rev shown", bodies)
	}
}

func TestOutboxEditAndDiscard(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, false)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "draft", State: draft.Proposed})
	if code := te.run("outbox", "edit", "1", "better text"); code != 0 {
		t.Fatalf("edit: exit %d: %s", code, te.err)
	}
	if got := te.draftState(t, db, d.ID); got.Text != "better text" || got.State != draft.Proposed {
		t.Fatalf("edited %+v", got)
	}
	if code := te.run("outbox", "discard", "1", "--reason", "stale"); code != 0 {
		t.Fatalf("discard: exit %d: %s", code, te.err)
	}
	if got := te.draftState(t, db, d.ID); got.State != draft.Discarded || got.Reason != "stale" {
		t.Fatalf("discarded %+v", got)
	}
	if code := te.run("outbox", "show", "1"); code != 0 || !strings.Contains(te.out.String(), "#1  reply  discarded") || !strings.Contains(te.out.String(), "Discarded   stale") {
		t.Fatalf("show: exit %d: %s", code, te.out)
	}
	if code := te.run("outbox", "frob"); code != 2 {
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

func TestOutboxApproveRefusesAnEditMadeWhileAsking(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "as shown", State: draft.Proposed})
	te.In = &editingReader{edit: func() {
		d.Text = "swapped while asking"
		if _, err := db.Drafts().Update(context.Background(), d, draft.Proposed); err != nil {
			t.Error(err)
		}
	}}
	if code := te.run("outbox", "approve", "1"); code == 0 || !strings.Contains(te.err.String(), "changed since you saw it") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := te.draftState(t, db, d.ID); got.State != draft.Proposed {
		t.Fatalf("approved %+v", got)
	}
}

func TestOutboxShowSaysWhenTheDestinationIsUnknown(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, false)
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "hi", State: draft.Proposed})
	if code := te.run("outbox", "show", "1"); code != 0 {
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

func TestOutboxNewSavesOrSends(t *testing.T) {
	target := "thread:" + threadUnit.Key
	t.Run("saved", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		db := outboxDaemon(t, te, false)
		knowThreads(t)
		if code := te.run("outbox", "new", target, "Friday works"); code != 0 || !strings.Contains(te.out.String(), "draft 1 saved to the Outbox for your approval") {
			t.Fatalf("exit %d: %s %s", code, te.out, te.err)
		}
		if got := te.draftState(t, db, 1); got.State != draft.Proposed || got.Kind != draft.Reply || got.Text != "Friday works" || got.Origin.Kind != draft.OriginUser || got.Unit != threadUnit {
			t.Fatalf("draft %+v", got)
		}
		if code := te.run("outbox", "new", target, "--react", ":eyes:"); code != 0 {
			t.Fatalf("react: exit %d: %s", code, te.err)
		}
		if got := te.draftState(t, db, 2); got.Kind != draft.React || got.Reaction != "eyes" {
			t.Fatalf("reaction %+v", got)
		}
	})
	t.Run("sent with --yes", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		db := outboxDaemon(t, te, false)
		knowThreads(t)
		if code := te.run("outbox", "new", target, "Shipped", "--send", "--yes"); code != 0 || !strings.Contains(te.out.String(), "draft 1 approved") {
			t.Fatalf("exit %d: %s %s", code, te.out, te.err)
		}
		if got := te.draftState(t, db, 1); got.State != draft.Approved {
			t.Fatalf("draft %+v", got)
		}
	})
	t.Run("--send asks at a terminal", func(t *testing.T) {
		te := newTestEnv(t, &fake.Manager{})
		outboxDaemon(t, te, false)
		knowThreads(t)
		if code := te.run("outbox", "new", target, "Shipped", "--send"); code == 0 || !strings.Contains(te.err.String(), "standard input is not one") {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		te = newTestEnv(t, &fake.Manager{})
		db := outboxDaemon(t, te, true)
		knowThreads(t)
		te.In = strings.NewReader("n\n")
		if code := te.run("outbox", "new", target, "Shipped", "--send"); code != 1 || !strings.Contains(te.out.String(), "Not sent; draft 1 stays in the Outbox") {
			t.Fatalf("exit %d: %s %s", code, te.out, te.err)
		}
		if got := te.draftState(t, db, 1); got.State != draft.Proposed {
			t.Fatalf("draft %+v", got)
		}
		te.In = strings.NewReader("y\n")
		if code := te.run("outbox", "new", target, "Shipped", "--send"); code != 0 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if got := te.draftState(t, db, 2); got.State != draft.Approved {
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
			if code := te.run(append([]string{"outbox", "new"}, strings.Split(args, "|")...)...); code == 0 || !strings.Contains(te.err.String(), want) {
				t.Errorf("%s: exit %d: %s", args, code, te.err)
			}
		}
	})
}

func TestOutboxListLaysOutForATerminal(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, false)
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Kind: draft.Nudge, Text: "@{github:ana}, any news?", State: draft.Proposed})
	if code := te.run("outbox", "--color", "always"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "NUDGE") || !strings.Contains(out, "\x1b[36m#1") || !strings.Contains(out, "@ana, any news?") {
		t.Fatalf("printed\n%s", out)
	}
}
