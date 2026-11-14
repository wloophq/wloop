package cli

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/lock"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// startEntries are the entries a new turn starts from. Each drives its turn
// with an intent whose role has a prepare and a guidance.max and checks
// that the prepare ran under the loop and the guidance was cut; a resume,
// or an intent with no guidance, checks that the prepare ran or none did.
// An entry tested in another package names that test, which must exist.
var startEntries = []struct {
	entry string
	run   func(*testing.T)
	// elsewhere is the package directory and test of an entry its own
	// package drives.
	elsewhere [2]string
}{
	{entry: "P1 by hand", run: TestAReviewByHandPreparesUnderTheLoopAndCutsItsGuidance},
	{entry: "P1a attach", run: TestAnAttachRunsItsRolesPrepare},
	{entry: "intake start", run: TestATurnByHandThroughAnIntakePreparesAtItsStart},
	{entry: "P2 action on a pull request", run: TestAnActionOnAPullRequestPreparesUnderTheLoopAndCutsItsGuidance},
	{entry: "P2 action on a unit", run: TestAnActionOnAUnitPreparesUnderTheLoopAndCutsItsGuidance},
	{entry: "run_now", run: TestRunNowPreparesUnderTheLoopAndHandsTheKeptGuidanceCut},
	{entry: "TrustAndRun", run: TestTrustAndRunPreparesUnderTheLoopAndCutsItsGuidance},
	{entry: "Release", run: TestAReleasedHeldRoundPreparesUnderTheDaemonsLoopAndCutsItsGuidance},
	{entry: "P3 round", run: TestAWorkflowRoundRunsItsRolesPrepare},
	{entry: "P3 non-round", run: TestAWorkflowNudgeLaunchesWithWhatItsPrepareFilled},
	{entry: "P3 unit", run: TestAWorkflowTurnOnAUnitPreparesUnderTheLoopAndCutsItsGuidance},
	{entry: "P3 headless", run: TestAHeadlessWorkflowTurnStartsThroughItsRolesPrepareOnItsCutGuidance},
	{entry: "daemon headless route", run: TestAHeadlessActionStartsThroughItsRolesPrepareOnItsCutGuidance},
	{entry: "chat", elsewhere: [2]string{"internal/core/messages", "TestAChatTurnRunsItsRolesPrepareOnItsCutGuidance"}},
	{entry: "probe", elsewhere: [2]string{"internal/daemon", "TestAProbeStartsThroughTheStarterAndTakesNoGuidance"}},
	{entry: "headless Again", elsewhere: [2]string{"internal/core/headless", "TestAHeadlessRunAgainResumesWithoutAPrepare"}},
}

func TestEveryEntryStartsThroughThePrepareAndTheCut(t *testing.T) {
	for _, e := range startEntries {
		t.Run(e.entry, func(t *testing.T) {
			if e.run != nil {
				e.run(t)
				return
			}
			if !declaresTest(t, filepath.Join("..", "..", e.elsewhere[0]), e.elsewhere[1]) {
				t.Fatalf("%s declares no %s", e.elsewhere[0], e.elsewhere[1])
			}
		})
	}
}

// declaresTest says whether a _test.go file of dir declares the test name.
func declaresTest(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
				return true
			}
		}
	}
	return false
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
