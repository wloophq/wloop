package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func writeUserWorkflow(t *testing.T, root, name, manifest string) {
	t.Helper()
	dir := filepath.Join(root, "workflows", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// workflowDoctorEnv is a test env whose profile adds an event workflow and
// a broken one, over a store where session-pr-finished ran and was refused
// and the event workflow's cursor stopped two hours ago.
func workflowDoctorEnv(t *testing.T) (*testEnv, *sqlite.DB) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	user := t.TempDir()
	writeUserWorkflow(t, user, "after-merge", `subject = "github"
when = 'trigger.event.kind == "merged"'

[trigger]
on = ["merged", "closed"]

[[step]]
use = "turn.dismiss"
reason = "merged"
`)
	writeUserWorkflow(t, user, "broken-one", `subject = "session"
when = 'true'

[trigger]
every = "sweep"

[[step]]
use = "session.explode"
`)
	te.cfg.Profile = user
	off := false
	te.cfg.Workflows = map[string]config.WorkflowConfig{"session-duplicate": {Enabled: &off}, "workspace-temporary": {Mode: "apply"}, "sesion-pr-finished": {Mode: "observe"}, "broken-one": {Mode: "observe"}}

	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	now := te.Now()
	sess := unit.Unit{Provider: unit.Session, Key: "term_1"}
	key := workflow.Key{Workflow: "session-pr-finished", Unit: sess, Value: "0123456789abcdef"}
	refused := func(attempt string, at time.Time) port.Event {
		return port.Event{Unit: sess, Kind: workflow.KindStepRefused, Key: key.ID() + ":" + attempt, Data: "the session is closed", At: at}
	}
	if err := db.Ledger().Record(ctx, workflow.Run{Key: key, Status: workflow.RunDone, At: now.Add(-2 * time.Hour)}, workflow.Item{ID: key.ID(), Key: key},
		refused("1", now.Add(-time.Hour)), refused("2", now.Add(-90*time.Minute)), refused("0", now.Add(-30*time.Hour))); err != nil {
		t.Fatal(err)
	}
	head, err := db.Events().Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Events().Advance(ctx, "after-merge", head, time.Time{}); err != nil {
		t.Fatal(err)
	}
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	other := workflow.Key{Workflow: "hygiene-orphan-answer", Unit: pr, Value: "fedcba9876543210"}
	if err := db.Ledger().Record(ctx, workflow.Run{Key: other, Status: workflow.RunRefused, At: now}, workflow.Item{ID: other.ID(), Key: other, Workflow: other.Workflow, Unit: pr, Decision: workflow.DecisionBlocked, Mode: workflow.ModeApply, At: now},
		port.Event{Unit: pr, Kind: "merged", Key: "m1", At: now.Add(-2 * time.Hour)}, port.Event{Unit: pr, Kind: "closed", Key: "c1", At: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	ws := unit.Unit{Provider: unit.Workspace, Key: "wt-1"}
	planned := workflow.Key{Workflow: "workspace-review-finished", Unit: ws, Value: "00112233aabbccdd"}
	if err := db.Ledger().Save(ctx, automation.PlanWrite{Workflows: []string{planned.Workflow}, Items: []workflow.Item{{ID: planned.ID(), Key: planned,
		Workflow: planned.Workflow, Unit: ws, Decision: workflow.DecisionPending, Default: workflow.OutcomeWait, Reason: "observe mode", Mode: workflow.ModeObserve, At: now}}}); err != nil {
		t.Fatal(err)
	}
	return te, db
}

func checkNamed(checks []api.DoctorCheck, name string) (api.DoctorCheck, bool) {
	i := slices.IndexFunc(checks, func(c api.DoctorCheck) bool { return c.Name == name })
	if i < 0 {
		return api.DoctorCheck{}, false
	}
	return checks[i], true
}

func TestDoctorReportsWorkflows(t *testing.T) {
	t.Parallel()
	te, db := workflowDoctorEnv(t)
	rep, err := te.doctorWith(db)(context.Background(), api.DoctorQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]api.DoctorCheck{
		"workflow session-pr-finished": {Mark: api.DoctorOK, Info: "apply, sweep, last ran 2h ago, 2 refusal(s) in the last day"},
		"workflow session-manual-quiet": {Mark: api.DoctorOK, Info: "apply, sweep, never ran, 0 refusal(s) in the last day; each item waits for you to apply it" +
			`; set "workflows": {"session-manual-quiet": {"approval": {"default": "run"}}} in ` + te.ConfigPath + " to run them without you"},
		"workflow workspace-review-finished": {Mark: api.DoctorInfo, Info: "observe, sweep, 1 item(s) planned and none run; see " + brand.Command + " plan --workflow workspace-review-finished" +
			`; set "workflows": {"workspace-review-finished": {"mode": "apply"}} in ` + te.ConfigPath + " to run them"},
		"workflow workspace-duplicate": {Mark: api.DoctorInfo, Info: "observe, sweep, 0 item(s) planned and none run; see " + brand.Command + " plan --workflow workspace-duplicate" +
			`; set "workflows": {"workspace-duplicate": {"mode": "apply"}} in ` + te.ConfigPath + " to run them"},
		"workflow workspace-temporary": {Mark: api.DoctorOK, Info: "apply, sweep, never ran, 0 refusal(s) in the last day"},
		"workflow after-merge":         {Mark: api.DoctorOK, Info: "apply, event, never ran, 0 refusal(s) in the last day"},
		"workflow session-duplicate":   {Mark: api.DoctorInfo, Info: "disabled by workflows.session-duplicate.enabled in " + te.ConfigPath},
	} {
		got, ok := checkNamed(rep.Workflows, name)
		if want.Name = name; !ok || got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	stuck, ok := checkNamed(rep.Workflows, "workflow after-merge cursor")
	if !ok || stuck.Mark != api.DoctorMissing || !strings.Contains(stuck.Info, "2 event(s) not handled, the oldest (merged) 2h ago") || !strings.Contains(stuck.Fix, "serve.err.log") {
		t.Errorf("stuck cursor %+v", stuck)
	}
	broken, ok := checkNamed(rep.Workflows, "workflow broken-one")
	if !ok || broken.Mark != api.DoctorMissing || !strings.Contains(broken.Info, "session.explode") || !strings.Contains(broken.Info, "every other workflow runs") {
		t.Errorf("broken %+v", broken)
	}

	if rep, err := te.doctorWith(db)(context.Background(), api.DoctorQuery{Repo: "acme/api"}); err != nil || rep.Workflows != nil {
		t.Errorf("profile show read the workflows: %+v %v", rep.Workflows, err)
	}

	te.Socket = serveJSON(t, map[string]any{"GET /api/health": api.Health{}, "GET " + api.AdminDoctor: rep})
	c, err := te.daemonClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, workflows := te.stateChecks(context.Background(), c)
	if len(workflows) != len(rep.Workflows) || !slices.ContainsFunc(workflows, func(c check) bool { return c.mark == markMissing && c.name == "workflow broken-one" }) {
		t.Errorf("rendered %+v", workflows)
	}
}

func TestDoctorNamesARevertedWorkflowOverride(t *testing.T) {
	t.Parallel()
	te, db := workflowDoctorEnv(t)
	writeUserWorkflow(t, te.cfg.Profile, "session-duplicate", "bogus = 1\n")
	rep, err := te.doctorWith(db)(context.Background(), api.DoctorQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := checkNamed(rep.Workflows, "workflow session-duplicate override")
	if !ok || got.Mark != api.DoctorMissing || !strings.Contains(got.Info, "runs as bundled") || !strings.Contains(got.Info, filepath.Join(te.cfg.Profile, "workflows", "session-duplicate", "workflow.toml")) {
		t.Fatalf("%+v in %+v", got, rep.Workflows)
	}
}

func TestDoctorWarnsOnAnUnknownWorkflowConfig(t *testing.T) {
	t.Parallel()
	te, db := workflowDoctorEnv(t)
	rep, err := te.doctorWith(db)(context.Background(), api.DoctorQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := checkNamed(rep.Workflows, "config workflows.sesion-pr-finished")
	if !ok || got.Mark != api.DoctorMissing || got.Info != "names no workflow, so it changes nothing" || !strings.Contains(got.Fix, te.ConfigPath) {
		t.Fatalf("%+v in %+v", got, rep.Workflows)
	}
	for _, name := range []string{"config workflows.session-duplicate", "config workflows.workspace-temporary", "config workflows.broken-one"} {
		if _, ok := checkNamed(rep.Workflows, name); ok {
			t.Errorf("%s warned on a known workflow", name)
		}
	}
}

// failingReads fails every workflow read.
type failingReads struct{ workflowReads }

func (failingReads) WorkflowActivity(context.Context, time.Time) (map[string]sqlite.WorkflowActivity, error) {
	return nil, errors.New("disk I/O error")
}

func TestDoctorKeepsTheStateChecksWhenAWorkflowReadFails(t *testing.T) {
	t.Parallel()
	te, db := workflowDoctorEnv(t)
	te.Rounds = sqlite.NewRounds(db)
	rep, err := te.doctorWith(failingReads{db})(context.Background(), api.DoctorQuery{})
	if err != nil {
		t.Fatalf("one failed workflow read failed the report: %v", err)
	}
	got, ok := checkNamed(rep.Workflows, "workflows")
	if !ok || got.Mark != api.DoctorMissing || !strings.Contains(got.Info, "disk I/O error") || rep.Checks == nil {
		t.Fatalf("%+v %+v", rep.Workflows, rep.Checks)
	}
}

func TestDoctorReportsAConfigItCannotRead(t *testing.T) {
	t.Parallel()
	te, db := workflowDoctorEnv(t)
	te.LoadConfig = func() (config.Config, error) { return config.Config{}, errors.New("invalid character '}'") }
	rep, err := te.doctorWith(db)(context.Background(), api.DoctorQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := checkNamed(rep.Workflows, "workflows config")
	if !ok || got.Mark != api.DoctorMissing || !strings.Contains(got.Info, "invalid character") || !strings.Contains(got.Info, "the profile's modes") {
		t.Fatalf("%+v", rep.Workflows)
	}
}
