package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// observed makes the rig's round one the github facts observe: its
// workspace is gone and the config sends none without it.
func (r *startRig) observed(round string) {
	r.planner().edit(r.sc.ref.String(), round, func(r *src.Round) { r.Observed, r.Workspace = true, src.WorkspaceRef{} })
}

func (r *startRig) count(kind string) int {
	n := 0
	for _, k := range r.rec.kinds() {
		if k == kind {
			n++
		}
	}
	return n
}

func TestAnObservedFollowupRecordsWouldReviewOncePerHead(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	r.observed(roundFollowup)
	for range 2 {
		got, err := r.run(t, automation.RunOpts{})
		if err != nil || got != "observed the follow-up: the author answered" {
			t.Fatalf("run = %q, %v", got, err)
		}
	}
	if n := r.count(dinv.KindWouldReview); n != 1 {
		t.Fatalf("%d would_review events, want 1: %v", n, r.rec.kinds())
	}
	if len(sc.ws.Prompts) != 0 || len(sc.ws.Writes) != 0 {
		t.Fatalf("an observed follow-up sent %v %v", sc.ws.Prompts, sc.ws.Writes)
	}
}

func TestAnObservedAddressRecordsWouldAddressOncePerFingerprint(t *testing.T) {
	t.Parallel()
	sc := addressScenario(t)
	r := newStartRig(t, sc, string(dintent.Address), "")
	r.observed(roundAddress)
	for range 2 {
		got, err := r.run(t, automation.RunOpts{})
		if err != nil || !strings.HasPrefix(got, "observed the ") {
			t.Fatalf("run = %q, %v", got, err)
		}
	}
	if n := r.count(dinv.KindWouldAddress); n != 1 {
		t.Fatalf("%d would_address events, want 1: %v", n, r.rec.kinds())
	}
	if len(sc.ws.Prompts) != 0 || len(sc.ws.Writes) != 0 {
		t.Fatalf("an observed address round sent %v %v", sc.ws.Prompts, sc.ws.Writes)
	}
}

func TestAnUntrustedFolderBlocksTheRoundUntilTheUserUnblocksIt(t *testing.T) {
	t.Parallel()
	sc := addressScenario(t)
	sc.ws.Sessions = nil
	sc.ws.Blocked = workspace.ReasonTrust
	home, file := untrustedHome(t)
	sc.te.cfg.AgentHome = home
	r := newStartRig(t, sc, string(dintent.Address), "")
	_, err := r.run(t, automation.RunOpts{})
	var refused automation.Refused
	if !asRefused(err, &refused) || !strings.HasPrefix(refused.Reason, "round blocked: ") {
		t.Fatalf("run = %v", err)
	}
	i := slices.IndexFunc(r.rec.events, func(e dinv.Event) bool { return e.Kind == dinv.KindRoundBlocked })
	if i < 0 || !strings.HasPrefix(r.rec.events[i].Data, "could not start: ") || !strings.HasSuffix(r.rec.events[i].Data, " is not trusted in "+home) {
		t.Fatalf("events %+v", r.rec.events)
	}
	unchanged(t, file)

	var out strings.Builder
	if err := (envActions{e: sc.env}).Unblock(context.Background(), r.u, daemon.Call{Out: &out}); err != nil {
		t.Fatal(err)
	}
	if want := sc.ref.String() + ": the blocked round waits for a new trigger\n"; out.String() != want {
		t.Fatalf("out %q, want %q", out.String(), want)
	}
	planned, _ := r.planner().round(sc.ref.String(), roundAddress)
	fp := planned.OnSent[ghsource.MarkAddressedReviews]
	if got := sc.te.loadRounds(t).Mark(sc.ref.String(), ghsource.MarkAddressedReviews); fp == "" || got != fp {
		t.Fatalf("fingerprint %q, want the blocked round's %q", got, fp)
	}
}

func TestARoundWaitsWhileItsTurnAsksTheUser(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, followupScenario(t), string(dintent.Review), roundFollowup)
	r.rec.questions = map[pr.Ref]map[string]bool{r.sc.ref: {string(dintent.Review): true}}
	if got := r.check(t, automation.CheckOpts{}); got != "its review turn asks you something" {
		t.Fatalf("refusal %q", got)
	}
	r.rec.questions = map[pr.Ref]map[string]bool{r.sc.ref: {string(dintent.Address): true}}
	if got := r.check(t, automation.CheckOpts{}); got != "" {
		t.Fatalf("an address question refused the review: %q", got)
	}
}

// askOn stores an asked turn of intent on the rig's unit, sent on head, and
// returns its seq.
func (r *startRig) askOn(t *testing.T, intent, role, head string) int64 {
	t.Helper()
	b := r.sc.te.bookOf(t, r.sc.ref)
	b.Unit = r.u
	b.Turns = append(b.Turns, dturn.Turn{Intent: dturn.Intent(intent), Role: role, ID: "q-" + head, Trigger: dturn.Automated, Head: head, Session: "term-" + head, State: dturn.Asked, SentAt: testNow})
	b.Records = append(b.Records, dturn.Record{Session: "term-" + head, Hash: "h-" + head, Head: head, At: testNow, Record: record.Record{State: record.Asked, Summary: "publish the approval?"}})
	saved, err := r.sc.te.repo.Save(context.Background(), b, port.Event{Unit: r.u, Kind: turns.KindTurnSent, Key: "q-" + head, At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	r.rec.questions = map[pr.Ref]map[string]bool{r.sc.ref: {intent: true}}
	q, _ := saved.ByID("q-" + head)
	return q.Seq
}

func TestAReviewQuestionOnThePlannedHeadHoldsTheRound(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, followupScenario(t), string(dintent.Review), roundFollowup)
	planned, ok := r.planner().round(r.sc.ref.String(), roundFollowup)
	if !ok || planned.Head == "" {
		t.Fatalf("planned %+v %v", planned, ok)
	}
	r.askOn(t, string(dintent.Review), "pr.review", planned.Head)
	if got := r.check(t, automation.CheckOpts{}); got != "its review turn asks you something" {
		t.Fatalf("refusal %q", got)
	}
}

func TestAReviewRoundOnANewerHeadReplacesTheQuestionAskedOnAnOlderOne(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, followupScenario(t), string(dintent.Review), roundFollowup)
	planned, _ := r.planner().round(r.sc.ref.String(), roundFollowup)
	seq := r.askOn(t, string(dintent.Review), "pr.review", "c-gone")
	if got := r.check(t, automation.CheckOpts{}); got != "" {
		t.Fatalf("a question on a head the pull request moved past: %q", got)
	}
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	b := r.sc.te.bookOf(t, r.sc.ref)
	q, _ := b.ByID("q-c-gone")
	next, ok := b.Newest(dturn.Intent(dintent.Review))
	if q.Seq != seq || q.State != dturn.Replaced || !ok || next.Seq == seq || next.Head != planned.Head || b.OpenQuestion(dturn.Intent(dintent.Review)) {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestAReviewQuestionWithAnAnswerOnItsWayHoldsTheRoundOnANewerHead(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, followupScenario(t), string(dintent.Review), roundFollowup)
	seq := r.askOn(t, string(dintent.Review), "pr.review", "c-gone")
	b := r.sc.te.bookOf(t, r.sc.ref)
	b.Answers = append(b.Answers, dturn.Answer{TurnSeq: seq, Text: "publish it", Route: dturn.Typed, At: testNow.Add(time.Minute)})
	if _, err := r.sc.te.repo.Save(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if got := r.check(t, automation.CheckOpts{}); got != "its review turn asks you something" {
		t.Fatalf("refusal %q", got)
	}
}

func TestAnAddressRoundWaitsOnItsQuestionWhateverTheHead(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, addressScenario(t), string(dintent.Address), roundAddress)
	r.askOn(t, string(dintent.Address), "pr.address", "c-the-agent-pushed")
	if got := r.check(t, automation.CheckOpts{}); got != "its address turn asks you something" {
		t.Fatalf("refusal %q", got)
	}
}

func TestOpenQuestionsAreTheIntentsWhoseNewestTurnAsks(t *testing.T) {
	t.Parallel()
	asked := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	answered := unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	books := map[unit.Unit]dturn.Book{
		asked: {Unit: asked, Turns: []dturn.Turn{
			{Seq: 1, Intent: dturn.Intent(dintent.Review), Role: "pr.review", State: dturn.Done},
			{Seq: 2, Intent: dturn.Intent(dintent.Address), Role: "pr.address", State: dturn.Asked},
		}},
		answered: {Unit: answered, Turns: []dturn.Turn{
			{Seq: 1, Intent: dturn.Intent(dintent.Review), Role: "pr.review", State: dturn.Asked},
			{Seq: 2, Intent: dturn.Intent(dintent.Review), Role: "pr.review", State: dturn.Done},
		}},
	}
	got := storeRecorder{}.OpenQuestions(books)
	want := map[pr.Ref]map[string]bool{prOf(asked): {string(dintent.Address): true}}
	if len(got) != 1 || len(got[prOf(asked)]) != 1 || !got[prOf(asked)][string(dintent.Address)] {
		t.Fatalf("open questions %v, want %v", got, want)
	}
}

func TestABriefThatGaveUpLetsTheRoundGoWithoutIt(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, followupScenario(t), string(dintent.Review), roundFollowup)
	r.briefs.rd = intake.Readiness{Go: true, Without: true, Note: "the intake gave up"}
	var p startPlan
	if refusal := r.steps.briefGate(context.Background(), r.u, &p, true); refusal != "" || p.briefMissing != "the intake gave up" {
		t.Fatalf("refusal %q, brief missing %q", refusal, p.briefMissing)
	}
}
