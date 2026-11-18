package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/turn"
)

// answeredQuestions is an agent whose home or checkout answered every
// question.
type answeredQuestions struct{ turns.Agent }

func (answeredQuestions) BlockedOn(string) string { return "" }

func (answeredQuestions) Explain(context.Context, turns.AgentSession, string) error { return nil }

func (answeredQuestions) Unanswered(context.Context, turns.AgentSession, string) bool { return false }

// blockRig is an Env whose recorder holds a start block on #3, read with
// a clock the test moves.
type blockRig struct {
	sc  sweepCase
	rec *memRecorder
	now time.Time
	ref pr.Ref
}

func newBlockRig(t *testing.T, key string) *blockRig {
	t.Helper()
	sc := followupScenario(t)
	r := &blockRig{sc: sc, rec: &memRecorder{}, now: testNow, ref: pr.Ref{Owner: "acme", Repo: "api", Number: 3}}
	sc.env.Rec, sc.env.Now = r.rec, func() time.Time { return r.now }
	r.block(key, testNow.Add(-time.Hour))
	return r
}

func (r *blockRig) block(key string, at time.Time) {
	r.rec.events = append(r.rec.events, dinv.Event{PR: r.ref, Kind: dinv.KindRoundBlocked, Key: key, At: at, Data: "folder not trusted"})
}

func (r *blockRig) unblocks() []dinv.Event {
	var out []dinv.Event
	for _, e := range r.rec.events {
		if e.Kind == dinv.KindRoundUnblocked {
			out = append(out, e)
		}
	}
	return out
}

const (
	trustBlockKey = "2026-10-21T14:00:00Z\ntrust\n/w/app\n/w/app/sub\n\n\nfirst\n\n"
	mcpBlockKey   = "2026-10-21T14:00:00Z\nagent-project-mcp\n/w/app\n\n\n\nfirst\n\n"
)

func TestTheHostLiftsAStartBlockOnceADayPassesBlind(t *testing.T) {
	t.Parallel()
	r := newBlockRig(t, trustBlockKey)
	r.sc.env.Agents = nil
	b := &startBlocks{e: r.sc.env}
	if err := b.Refresh(context.Background()); err != nil || len(r.unblocks()) != 0 {
		t.Fatalf("without agents: unblocked %v, %v", r.unblocks(), err)
	}
	r.sc.env.Agents = map[string]turns.Agent{}
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !b.Due().Equal(testNow.Add(23*time.Hour)) || len(r.unblocks()) != 0 {
		t.Fatalf("due %v, unblocked %v; want the blind block's day", b.Due(), r.unblocks())
	}
	r.now = testNow.Add(24 * time.Hour)
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.unblocks(); len(got) != 1 || got[0].PR != r.ref {
		t.Fatalf("a day later unblocked %v", got)
	}

	r.now = testNow.Add(30 * time.Hour)
	r.block(trustBlockKey, testNow.Add(29*time.Hour))
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !b.Due().Equal(testNow.Add(53 * time.Hour)) {
		t.Fatalf("blocked again: due %v", b.Due())
	}
}

func TestTheHostLiftsAStartBlockTheCheckoutAnswers(t *testing.T) {
	t.Parallel()
	r := newBlockRig(t, mcpBlockKey)
	cfg, err := r.sc.env.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	r.sc.env.Agents = map[string]turns.Agent{cfg.Agent: answeredQuestions{}}
	if err := (&startBlocks{e: r.sc.env}).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.unblocks(); len(got) != 1 || got[0].Data != "the agent's question is answered" {
		t.Fatalf("unblocked %v", got)
	}
}

// noStartBlockKey is a block of a round whose session never started, recorded an hour before testNow and
// retrying an hour after it.
var noStartBlockKey = inventory.StartBlock{Folder: "/w/app", Round: "first", RetryAt: testNow.Add(time.Hour)}.Key(testNow.Add(-time.Hour))

func TestASessionThatNeverStartedHoldsItsRoundForTheRetryWait(t *testing.T) {
	t.Parallel()
	r := newBlockRig(t, noStartBlockKey)
	cfg, err := r.sc.env.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	r.sc.env.Agents = map[string]turns.Agent{cfg.Agent: answeredQuestions{}}
	b := &startBlocks{e: r.sc.env}
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.unblocks(); len(got) != 0 || !b.Due().Equal(testNow.Add(time.Hour)) {
		t.Fatalf("unblocked %v, due %v; want the block held until the retry", got, b.Due())
	}
	r.now = testNow.Add(time.Hour)
	if err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.unblocks(); len(got) != 1 {
		t.Fatalf("after the retry wait unblocked %v", got)
	}
}

func TestAFollowUpWhoseSessionNeverStartedIsBlockedNotRetriedAtOnce(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	rec := &memRecorder{}
	sc.env.Rec = rec
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 3}
	cfg := config.Defaults()
	cfg.Turns.NoStartWait = config.Days(3 * time.Hour)
	s := &sweep{e: sc.env, cfg: cfg, now: testNow, st: round.New()}
	st := step{kind: stepFollowup, ws: wsdomain.Workspace{ID: "ws-3", Path: "/w/app", DisplayName: "app"}, pr: ref}
	s.noteStartProblem(context.Background(), st, fmt.Errorf("%w; closed its session s-1", errNoMCP{"/home/me/.claude"}))
	var blocks []inventory.StartBlock
	for _, e := range rec.events {
		if b, ok := inventory.StartBlockOf(port.Event{Kind: e.Kind, Key: e.Key}); ok {
			blocks = append(blocks, b)
		}
	}
	if len(blocks) != 1 || blocks[0].Question != "" || !blocks[0].RetryAt.Equal(testNow.Add(3*time.Hour)) || blocks[0].Folder != "/w/app" {
		t.Fatalf("blocks %+v, events %+v", blocks, rec.events)
	}
}

func TestARoundOnAUnitWhoseSessionNeverStartedIsBlocked(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	rec := &memRecorder{}
	sc.env.Rec = rec
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 4}
	cfg := config.Defaults()
	cfg.Turns.NoStartWait = config.Days(90 * time.Minute)
	run := unitRun{def: dintent.Def{Name: "act"}, cfg: cfg, r: turn.Request{Unit: ref.Unit()}}
	sc.env.blockUnit(context.Background(), run, wsdomain.Workspace{Path: "/w/api"}, fmt.Errorf("%w; closed its session s-1", errNoMCP{"/home/me/.claude"}))
	if len(rec.events) != 1 {
		t.Fatalf("events %+v", rec.events)
	}
	b, ok := inventory.StartBlockOf(port.Event{Kind: rec.events[0].Kind, Key: rec.events[0].Key})
	if !ok || b.Question != "" || !b.RetryAt.Equal(sc.env.Now().Add(90*time.Minute)) || b.Folder != "/w/api" || b.Round != "act" {
		t.Fatalf("block %+v", b)
	}
}

func TestARoundByHandWhoseSessionNeverStartsKeepsItsNoStartBlock(t *testing.T) {
	t.Parallel()
	r := newBlockRig(t, noStartBlockKey)
	err := r.sc.env.liftPassedBlock(context.Background(), r.ref.Unit(), fmt.Errorf("%w; closed its session s-1", errNoMCP{"/home/me/.claude"}))
	if !errors.As(err, new(errNoMCP)) || len(r.unblocks()) != 0 {
		t.Fatalf("err %v, unblocked %v; want the no-start block kept", err, r.unblocks())
	}
	r2 := newBlockRig(t, trustBlockKey)
	r2.sc.env.liftPassedBlock(context.Background(), r2.ref.Unit(), errNoMCP{"/home/me/.claude"})
	if len(r2.unblocks()) != 1 {
		t.Fatalf("a session past the trust question kept its block: %v", r2.unblocks())
	}
}

func TestABlindBlockWaitsAsLongAsTheConfigSays(t *testing.T) {
	t.Parallel()
	b := inventory.StartBlock{Question: turns.QuestionTrust, Folder: "/w/app"}
	if cleared, until := answeredBlock(context.Background(), nil, b, "", testNow, testNow.Add(time.Hour), 6*time.Hour); cleared != "" || !until.Equal(testNow.Add(6*time.Hour)) {
		t.Fatalf("cleared %q, until %v", cleared, until)
	}
	if cleared, _ := answeredBlock(context.Background(), nil, b, "", testNow, testNow.Add(6*time.Hour), 6*time.Hour); cleared == "" {
		t.Fatal("the blind block holds past its wait")
	}
}
