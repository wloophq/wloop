package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/lock"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// startEntries are the entries of this package a new turn starts from.
// Each drives its turn with an intent whose role has a prepare and a
// guidance.max and checks that the prepare ran under the loop and the
// guidance was cut. The decision log names the tests of the chat and the
// probe, which their own packages drive.
var startEntries = []struct {
	entry string
	run   func(*testing.T)
}{
	{"P1 by hand", TestAReviewByHandPreparesUnderTheLoopAndCutsItsGuidance},
	{"P1a attach", TestAnAttachRunsItsRolesPrepare},
	{"intake start", TestATurnByHandThroughAnIntakePreparesAtItsStart},
	{"P2 action on a pull request", TestAnActionOnAPullRequestPreparesUnderTheLoopAndCutsItsGuidance},
	{"P2 action on a unit", TestAnActionOnAUnitPreparesUnderTheLoopAndCutsItsGuidance},
	{"run_now", TestRunNowPreparesUnderTheLoopAndHandsTheKeptGuidanceCut},
	{"TrustAndRun", TestTrustAndRunPreparesUnderTheLoopAndCutsItsGuidance},
	{"Release", TestAReleasedHeldRoundPreparesUnderTheDaemonsLoopAndCutsItsGuidance},
	{"P3 round", TestAWorkflowRoundRunsItsRolesPrepare},
	{"P3 non-round", TestAWorkflowNudgeLaunchesWithWhatItsPrepareFilled},
	{"P3 unit", TestAWorkflowTurnOnAUnitPreparesUnderTheLoopAndCutsItsGuidance},
	{"P3 headless", TestAHeadlessWorkflowTurnStartsThroughItsRolesPrepareOnItsCutGuidance},
	{"daemon headless route", TestAHeadlessActionStartsThroughItsRolesPrepareOnItsCutGuidance},
}

func TestEveryEntryOfTheCLIStartsThroughThePrepareAndTheCut(t *testing.T) {
	for _, e := range startEntries {
		t.Run(e.entry, e.run)
	}
}

func TestAReviewByHandPreparesUnderTheLoopAndCutsItsGuidance(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	hp := &heldPreparer{mu: te.loop}
	preparedIntents(te, te.loop, hp, dintent.Review)
	if code := te.run(append([]string{"instructions", "review", r7.URL(), "--"}, strings.Fields(longAsk)...)...); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Equal(hp.held, []bool{true}) {
		t.Fatalf("prepares under the held loop mutex %v", hp.held)
	}
	if b := bookOf(t, te, r7); len(b.Turns) != 1 || b.Turns[0].Guidance != cutAsk {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestAnAttachRunsItsRolesPrepare(t *testing.T) {
	te := attachEnv(t)
	mu := &lock.Mutex{}
	hp := &heldPreparer{mu: mu}
	preparedIntents(te, mu, hp, "attach")
	inSession(te, "term_1")
	if code := te.run("task", "attach", "task:"+string(attachTask)); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Equal(hp.held, []bool{true}) {
		t.Fatalf("prepares under the held loop mutex %v", hp.held)
	}
}

func TestAWorkflowTurnOnAUnitPreparesUnderTheLoopAndCutsItsGuidance(t *testing.T) {
	r := newUnitStartRig(t, implementIntent)
	mu := &lock.Mutex{}
	hp := &heldPreparer{mu: mu}
	preparedIntents(r.te, mu, hp, "implement")
	if _, err := r.steps.Run(context.Background(), r.st, thread1, r.why, automation.RunOpts{Guidance: longAsk}); err != nil {
		t.Fatalf("%v\n%s", err, r.te.err)
	}
	if !slices.Equal(hp.held, []bool{true}) {
		t.Fatalf("prepares under the held loop mutex %v", hp.held)
	}
	if b := r.book(t, thread1); len(b.Turns) != 1 || b.Turns[0].Guidance != cutAsk {
		t.Fatalf("turns %+v", b.Turns)
	}
}
