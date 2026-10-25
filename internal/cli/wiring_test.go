package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// seedBriefOf stores a current brief of ref with tags.
func seedBriefOf(t *testing.T, st *inventory.Store, ref github.Ref, tags ...string) int64 {
	t.Helper()
	ctx := context.Background()
	version := brief.Version(brief.DefaultTaxonomy)
	start := testNow.Add(-time.Minute)
	run := dinv.BriefRun{PR: ref, Trigger: "wanted", Version: version, AttemptOf: "a", StartedAt: start}
	id, err := st.StartBriefRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	run.ID, run.EndedAt, run.Outcome, run.Model, run.Tags = id, start.Add(12*time.Second), "ok", "haiku", tags
	run.Brief = `{"goal":"Export in batches","context":"exports time out","kind":"feature","size":"m","risk":"medium","risk_why":"touches the export","open_questions":[],"tags":[]}`
	if err := st.EndBriefRun(ctx, run, dinv.BriefRecord{PR: ref, AttemptOf: "a"}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTurnKeepsItsBriefRun(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	te.cfg.Brief.Enabled = true
	id := seedBriefOf(t, te.Store, r7)
	sent, err := te.sendTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.ByHand, Unit: r7.Unit(), Payload: &turn.PR{Title: "Batch export"}})
	if err != nil {
		t.Fatal(err)
	}
	if sent.send == nil || sent.send.BriefRun != id {
		t.Fatalf("send %+v, want brief run %d", sent.send, id)
	}
	if _, err := te.Turns.Send(context.Background(), *sent.send); err != nil {
		t.Fatal(err)
	}
	books, err := te.Turns.Store.Books(context.Background(), pr.Ref(r7).Unit())
	if err != nil {
		t.Fatal(err)
	}
	b := books[pr.Ref(r7).Unit()]
	if got, _ := b.ByID(sent.ID); got == nil || got.BriefRun != id {
		t.Fatalf("stored turn %+v", got)
	}
}

func TestTurnInstructionsCarryATagBlock(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "tags", "change=refac", "intent=review.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Behavior must not change; ask for the test that proves it.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	te.cfg.Profile, te.cfg.Brief.Enabled = dir, true
	seedBriefOf(t, te.Store, r7, "change:refac")
	_, out, err := te.renderTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.ByHand, Unit: r7.Unit(), Payload: &turn.PR{Title: "Batch export"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "Behavior must not change") {
		t.Fatalf("the tag block is missing:\n%s", out.Text)
	}
	seedBriefOf(t, te.Store, r7, "change:feature")
	if _, out, _ := te.renderTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.ByHand, Unit: r7.Unit(), Payload: &turn.PR{Title: "Batch export"}}); strings.Contains(out.Text, "Behavior must not change") {
		t.Fatal("a block of another tag is in the turn")
	}
}

func TestATrimmedGuideIsRecorded(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	rec := &memRecorder{}
	te.Rec = rec
	te.recordTrimmed(context.Background(), r7.Unit(), "r1", []string{"user: tags/lang=go/intent=review.md"})
	te.recordTrimmed(context.Background(), r7.Unit(), "r2", nil)
	if len(rec.events) != 1 || rec.events[0].Kind != kindGuideTrimmed || rec.events[0].Key != "r1" || rec.events[0].Data != "user: tags/lang=go/intent=review.md" {
		t.Fatalf("events %+v", rec.events)
	}
}

func TestTheReviewGuidesTrimmedBlocksArePrefixed(t *testing.T) {
	t.Parallel()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	address, _ := l.Registry.Get(dintent.Address)
	c := turn.Context{Def: address, Guide: profile.Guide{Dropped: []string{"user: a.md"}},
		Guides: map[string]profile.Guide{"address": {Dropped: []string{"user: a.md"}}, "review": {Dropped: []string{"user: r.md"}}}}
	if got := turn.Trimmed(c); !slices.Equal(got, []string{"user: a.md", "review guide: user: r.md"}) {
		t.Fatalf("%v", got)
	}
}

func TestRestartDrivesOpenIntakes(t *testing.T) {
	t.Parallel()
	te := newServeEnv(t)
	var waiting, starting intake.Intake
	seedStore(t, te.StateDir, func(ctx context.Context, db *sqlite.DB) {
		var err error
		at := testNow.Add(-time.Minute)
		if waiting, _, err = db.Intakes().Accept(ctx, intake.Intake{Unit: r7.Unit(), Intent: dturn.Intent(dintent.Review), Source: "cli", Request: []byte(`{"action":"review"}`), State: intake.Briefing, AcceptedAt: at}); err != nil {
			t.Fatal(err)
		}
		other := pr.Ref{Owner: "acme", Repo: "api", Number: 8}
		if starting, _, err = db.Intakes().Accept(ctx, intake.Intake{Unit: other.Unit(), Intent: dturn.Intent(dintent.Review), Source: "cli", Request: []byte(`{"action":"review"}`), State: intake.Starting, AcceptedAt: at}); err != nil {
			t.Fatal(err)
		}
	})
	serveUntil(t, te, "the intakes recovered", func(db *sqlite.DB) bool {
		w, _, _ := db.Intakes().Intake(context.Background(), waiting.Seq)
		s, _, _ := db.Intakes().Intake(context.Background(), starting.Seq)
		return !w.State.Open() && s.State == intake.Failed && s.Error == intake.NoteStopped
	})
}

func TestServeStartsTheWantedBriefs(t *testing.T) {
	t.Parallel()
	te := newServeEnv(t)
	seedR7(t, te)
	te.cfg.Brief.Enabled = true
	te.Agents = map[string]turns.Agent{"claude": claude.Agent{AskSh: func(context.Context, string, []string, string, string, ...string) ([]byte, error) {
		return nil, errors.New("claude: API Error: 529 Overloaded")
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--mcp-port", strconv.Itoa(freePort(t))}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	h := waitHealth(t, te.Env, "the daemon", func(api.Health) bool { return true })
	if h.Briefs == nil || h.Briefs.Unavailable != "" {
		t.Fatalf("briefs %+v", h.Briefs)
	}
	res, err := client.New(te.Socket).Ask(ctx, r7.Unit(), "review", api.ActionRequest{Source: "cli"})
	if err != nil || res.Intake == nil {
		t.Fatalf("%+v %v", res, err)
	}
	db, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	eventually(t, "a wanted brief run", func() bool {
		var n int
		return db.R().QueryRowContext(context.Background(), `SELECT count(*) FROM brief_runs WHERE trigger = 'wanted' AND number = 7`).Scan(&n) == nil && n > 0
	})
}

func TestServeReportsBriefsItCannotMake(t *testing.T) {
	t.Parallel()
	te := newServeEnv(t)
	te.cfg.Brief.Enabled = true
	te.Agents = map[string]turns.Agent{"claude": plainKind{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--mcp-port", strconv.Itoa(freePort(t))}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	h := waitHealth(t, te.Env, "the daemon", func(api.Health) bool { return true })
	if h.Briefs == nil || !strings.Contains(h.Briefs.Unavailable, "cannot answer headless calls") {
		t.Fatalf("briefs %+v", h.Briefs)
	}
}
