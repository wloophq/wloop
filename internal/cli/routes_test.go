package cli

import (
	"context"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
	ghplan "github.com/wloophq/wloop/plugins/github/plan"
)

// effects are what a round left in the round state and the activity log.
type effects struct {
	fingerprint  bool
	checksHead   string
	conflictHead string
	events       []string
}

func effectsOf(t *testing.T, te *testEnv, ref pr.Ref) effects {
	t.Helper()
	st := te.loadRounds(t)
	key := ref.String()
	got := effects{fingerprint: st.Mark(key, ghplan.MarkReviewed)+st.Mark(key, ghsource.MarkAddressedReviews) != "", checksHead: st.Mark(key, ghsource.MarkAddressedChecks), conflictHead: st.Mark(key, ghsource.MarkAddressedConflict)}
	evs, err := te.Store.Events(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Kind == dinv.KindRoundSent || ev.Kind == dinv.KindNudged {
			got.events = append(got.events, ev.Kind)
		}
	}
	return got
}

// storedTurn saves a turn of intent on ref that a resume continues, and
// returns its seq.
func storedTurn(t *testing.T, te *testEnv, ref pr.Ref, intent dintent.Name, inputs map[string]any) int64 {
	t.Helper()
	key := pr.Ref(ref)
	b := dturn.Book{Unit: key.Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(intent), ID: "r0", Trigger: dturn.ByHand, Head: "c1", State: dturn.Unrecorded, SentAt: testNow, Inputs: inputs}}}
	saved, err := te.repo.Save(context.Background(), b, port.Event{Unit: key.Unit(), Kind: turns.KindTurnSent, Key: "r0", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	return saved.Turns[0].Seq
}

var nudgeInputs = map[string]any{"situation": "Waiting on: bob.", "waits_on_maintainers": true, "who": []string{"bob"}}

func TestEachRouteRunsTodaysEffects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	byHand := func(intent dintent.Name, ws wsdomain.Workspace, inputs map[string]any) func(t *testing.T) (*testEnv, pr.Ref) {
		return func(t *testing.T) (*testEnv, pr.Ref) {
			_, te := handScenario(t, ws)
			te.Turns.Starter.Preparer = startPreparer{&fakePreparer{}}
			cwd := te.Harness.Cwd()
			_, err := te.byHand()(ctx, api.ByHandRequest{Intent: string(intent), URL: r7.URL(), Cwd: cwd, Inputs: inputs}, "term_1")
			if err != nil {
				t.Fatalf("%v\n%s", err, te.err)
			}
			return te, r7
		}
	}
	resume := func(intent dintent.Name, m func() workspace.Manager, setup func(*testEnv) pr.Ref, inputs map[string]any) func(t *testing.T) (*testEnv, pr.Ref) {
		return func(t *testing.T) (*testEnv, pr.Ref) {
			te := newTestEnv(t, m())
			te.Rec = storeRecorder{te.Store}
			ref := setup(te)
			if err := te.syncInventory(ctx); err != nil {
				t.Fatal(err)
			}
			seq := storedTurn(t, te, ref, intent, inputs)
			if _, err := te.launchAdmitted(ctx, turns.Launch{Unit: pr.Ref(ref).Unit(), Intent: dturn.Intent(intent), ResumeOf: seq, Answer: "go on"}); err != nil {
				t.Fatalf("%v\n%s", err, te.err)
			}
			return te, ref
		}
	}
	review := func(te *testEnv) pr.Ref {
		te.gh.basics = openBasics
		authorComment := obj{"id": "c", "createdAt": "2026-10-08T13:00:00Z", "author": obj{"login": "alice"}}
		myReview := obj{"id": "r", "state": "CHANGES_REQUESTED", "submittedAt": myReviewAt, "author": obj{"login": "me"}, "commit": obj{"oid": "c2"}}
		te.gh.reviewer = mustJSON(t, obj{"data": obj{"viewer": obj{"login": "me"}, "pr0": reviewerPR(7, "OPEN", []obj{myReview}, []obj{authorComment})}})
		return r7
	}
	mine := func(te *testEnv) pr.Ref {
		te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
		te.git.branch["/worktrees/feat/mine"] = "feat/mine"
		te.git.ancestor["/worktrees/feat/mine"] = true
		return r21
	}
	reviewWS, ownWS := wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7}, wsdomain.Workspace{ID: "ws-1", DisplayName: "feat/x", LinkedPR: 7}
	warm := func() workspace.Manager { return warmReviewWorkspace() }
	own := func() workspace.Manager { return myPRManager() }
	for _, c := range []struct {
		name string
		run  func(t *testing.T) (*testEnv, pr.Ref)
		want effects
	}{
		{"session route, review", byHand(dintent.Review, reviewWS, nil), effects{events: []string{dinv.KindRoundSent}}},
		{"session route, address", byHand(dintent.Address, ownWS, nil), effects{fingerprint: true, checksHead: "c2", conflictHead: "c2", events: []string{dinv.KindRoundSent}}},
		{"session route, nudge", byHand(dintent.Nudge, reviewWS, nudgeInputs), effects{events: []string{dinv.KindNudged}}},
		{"resume, review", resume(dintent.Review, warm, review, nil), effects{fingerprint: true}},
		{"resume, address", resume(dintent.Address, own, mine, nil), effects{fingerprint: true, checksHead: "c21", conflictHead: "c21"}},
		{"resume, nudge", resume(dintent.Nudge, warm, review, nudgeInputs), effects{events: []string{dinv.KindNudged}}},
		{"held Run now, review", func(t *testing.T) (*testEnv, pr.Ref) {
			sc := followupScenario(t)
			exhaustCap(t, sc)
			sc.te.gh.basics = openBasics
			if err := (envActions{sc.env, nil}).Run(ctx, sc.ref.Unit(), daemon.Call{Out: io.Discard}); err != nil {
				t.Fatal(err)
			}
			return sc.te, sc.ref
		}, effects{fingerprint: true}},
		{"held Run now, address", func(t *testing.T) (*testEnv, pr.Ref) {
			te := newTestEnv(t, myPRManager())
			ref := mine(te)
			if err := (envActions{te.Env, nil}).Run(ctx, ref.Unit(), daemon.Call{Out: io.Discard}); err != nil {
				t.Fatalf("%v\n%s", err, te.err)
			}
			return te, ref
		}, effects{fingerprint: true, checksHead: "c21", conflictHead: "c21"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			te, ref := c.run(t)
			if got := effectsOf(t, te, ref); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("effects %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestAResumeRestoresPersistedInputs(t *testing.T) {
	t.Parallel()
	m := warmReviewWorkspace()
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	seq := storedTurn(t, te, r7, dintent.Nudge, nudgeInputs)
	if _, err := te.launchAdmitted(context.Background(), turns.Launch{Unit: pr.Ref(r7).Unit(), Intent: dturn.Intent(dintent.Nudge), ResumeOf: seq, Answer: "go on"}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	b := te.bookOf(t, r7)
	if len(b.Turns) != 2 || b.Turns[1].ResumeOf != seq {
		t.Fatalf("turns %+v", b.Turns)
	}
	in := b.Turns[1].Inputs
	if in["situation"] != "Waiting on: bob." || in["waits_on_maintainers"] != true || !slices.Equal(inputList(in["who"]), []string{"bob"}) {
		t.Fatalf("inputs %#v", in)
	}
	if len(m.Prompts) != 1 || !strings.Contains(turnText(t, te, m.Prompts[0].Text), "Waiting on: bob.") {
		t.Fatalf("prompts %+v", m.Prompts)
	}
}

func TestHeldRunNowOnAnAdoptedPullRequestIsTheAuthors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	te := newTestEnv(t, myPRManager())
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "jo"})
	te.Rec = storeRecorder{te.Store}
	gh, err := te.forgeBasics(ctx, r21)
	if err != nil {
		t.Fatal(err)
	}
	role := func() string {
		t.Helper()
		facts, err := te.heldRoleFacts(ctx, te.cfg, nil, nil, r21, gh)
		if err != nil {
			t.Fatal(err)
		}
		return catalog.WorkspaceRole(facts)
	}
	if got := role(); got != catalog.RoleReview {
		t.Fatalf("someone else's pull request: role %s", got)
	}
	if err := inventorytest.Of(te.Store).SetOverride(ctx, dinv.Override{PR: r21, Adopted: true}); err != nil {
		t.Fatal(err)
	}
	if got := role(); got != catalog.RoleAuthor {
		t.Fatalf("an adopted pull request: role %s", got)
	}
}

func TestAResumeMarksAddressedWithoutRecordingTheRound(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, myPRManager())
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
	te.git.branch["/worktrees/feat/mine"] = "feat/mine"
	te.git.ancestor["/worktrees/feat/mine"] = true
	seq := storedTurn(t, te, r21, dintent.Address, nil)
	if _, err := te.launchAdmitted(context.Background(), turns.Launch{Unit: pr.Ref(r21).Unit(), Intent: dturn.Intent(dintent.Address), ResumeOf: seq, Answer: "go on"}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if got := effectsOf(t, te, r21); !reflect.DeepEqual(got, effects{fingerprint: true, checksHead: "c21", conflictHead: "c21"}) {
		t.Fatalf("effects %+v", got)
	}
}

// roundMarks are the marks a review or address round leaves for the
// planner on ref.
func roundMarks(t *testing.T, te *testEnv, ref pr.Ref) map[string]string {
	t.Helper()
	st := te.loadRounds(t)
	out := map[string]string{}
	for _, name := range []string{ghplan.MarkReviewed, ghsource.MarkAddressedReviews, ghsource.MarkAddressedChecks, ghsource.MarkAddressedConflict} {
		if v := st.Mark(ref.String(), name); v != "" {
			out[name] = v
		}
	}
	return out
}

func TestASentRoundLeavesThePlannerTheMarksItsPlanNamed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, intent, stage, round string
		scenario                   func(*testing.T) sweepCase
	}{
		{"sweep follow-up", string(dintent.Review), roundFollowup, roundFollowup, followupScenario},
		{"sweep address", string(dintent.Address), "", roundAddress, addressScenario},
	} {
		t.Run(c.name, func(t *testing.T) {
			sc := c.scenario(t)
			r := newStartRig(t, sc, c.intent, c.stage)
			planned, ok := r.planner().round(sc.ref.String(), c.round)
			if !ok {
				t.Fatalf("no %s planned", c.round)
			}
			if _, err := r.run(t, automation.RunOpts{}); err != nil {
				t.Fatal(err)
			}
			if got := roundMarks(t, sc.te, sc.ref); !maps.Equal(got, planned.OnSent) {
				t.Fatalf("marks %v, want exactly the plan's %v", got, planned.OnSent)
			}
			if _, due := offeredRounds(t, sc).round(sc.ref.String(), c.round); due {
				t.Fatalf("the %s is planned again after it was sent", c.round)
			}
		})
	}
}

func TestAHeldRoundRunNowLeavesThePlannerTheReviewsItCovered(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	exhaustCap(t, sc)
	sc.te.gh.basics = openBasics
	planned, ok := plannedRounds(t, sc).round(sc.ref.String(), roundFollowup)
	if !ok {
		t.Fatal("no follow-up planned")
	}
	if err := (envActions{sc.env, nil}).Run(context.Background(), sc.ref.Unit(), daemon.Call{Out: io.Discard}); err != nil {
		t.Fatalf("%v\n%s", err, sc.te.err)
	}
	if got := roundMarks(t, sc.te, sc.ref); !maps.Equal(got, planned.OnSent) {
		t.Fatalf("marks %v, want the reviews the held follow-up covered %v", got, planned.OnSent)
	}
	if _, due := offeredRounds(t, sc).round(sc.ref.String(), roundFollowup); due {
		t.Fatal("the follow-up is planned again after run now sent it")
	}
}
