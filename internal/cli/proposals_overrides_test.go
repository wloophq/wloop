package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// firstDayClosure runs the default workflows over the scenario's first tick and
// returns the closure the seeded close entry gets.
func firstDayClosure(t *testing.T, r *replay) draft.Draft {
	t.Helper()
	p := newWorkflowProposer(r)
	r.scenario(r.now)
	if err := p.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds, err := r.svc.List(context.Background(), outbox.Filter{Workflow: "slack-closure"})
	if err != nil || len(ds) != 1 {
		t.Fatalf("closures %+v, %v", ds, err)
	}
	return ds[0]
}

func TestAPortugueseClosureTemplateYieldsAPortugueseClosure(t *testing.T) {
	t.Parallel()
	r := newReplay(t)
	userProfile(t, r.te, map[string]string{"workflows/slack-closure/text.md.tmpl": "Encerrando:\n{{range .Items}}\n- {{.Line}}: {{or .Resolution \"resolvido\"}}{{end}}"})
	d := firstDayClosure(t, r)
	if want := "Encerrando:\n\n- Item d1 of the thread: done\n- Item d2 of the thread: resolvido"; d.Text != want {
		t.Fatalf("closure %q, want %q", d.Text, want)
	}
}

func TestAReactionClosureClosesWithACheckMark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "workflows", "slack-closure", "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(string(b), `purpose = "close"`, "purpose = \"close\"\ndelivery = \"reaction\"\nreaction = \"white_check_mark\"\ntarget = \"item\"", 1)
	userProfile(t, r.te, map[string]string{"workflows/slack-closure/workflow.toml": manifest})
	if err := r.db.Extraction().Save(ctx, sqlite.ExtractionSave{States: map[unit.Unit]sqlite.ExtractionState{
		rpD: {Verdict: "needs_closure", StartedByViewer: true, Watched: true, At: r.now}}}); err != nil {
		t.Fatal(err)
	}
	d := firstDayClosure(t, r)
	if d.Delivery != draft.Reaction || d.Reaction != "white_check_mark" || d.Target != "https://example.test/d1" || d.Text != "" {
		t.Fatalf("closure %+v", d)
	}
	if _, err := r.svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, failed, err := r.svc.Send(ctx); err != nil || failed > 0 {
		t.Fatalf("send: %d failed, %v", failed, err)
	}
	st, err := r.db.Extraction().States(ctx, []unit.Unit{rpD})
	if err != nil || !st[rpD].ClosureMarked {
		t.Fatalf("the ✅ closed nothing: %+v, %v", st[rpD], err)
	}
}

func TestALegacyProposalApprovalLoadsWithAWarningAndApplies(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(file, []byte(`{"proposals": {"closure": {"approval": {"mode": "window", "window": "2h"}},
		"github-nudge": {"approval": {"mode": "none"}}}, "workflows": {"slack-ask-owner": {"draft_approval": {"mode": "none"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(file, Defaults())
	if err != nil {
		t.Fatalf("a legacy approval fails the load: %v", err)
	}
	te := newTestEnv(t, nil)
	var logged []string
	te.wireWorkflows(&serveWiring{Cfg: cfg, Log: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }})
	if len(logged) != 1 || !strings.Contains(logged[0], "proposals.closure.approval") || !strings.Contains(logged[0], "workflows.slack-closure.draft_approval") {
		t.Fatalf("warnings %q", logged)
	}
	always := draft.Approval{Mode: draft.Always}
	if a := draftApproval(cfg, workflow.Workflow{Name: "slack-closure", DraftApproval: &always}); a.Mode != draft.Window || a.Window != 2*time.Hour {
		t.Errorf("the legacy approval does not apply: %+v", a)
	}
	if a := draftApproval(cfg, workflow.Workflow{Name: "slack-ask-owner", DraftApproval: &always}); a.Mode != draft.None {
		t.Errorf("the workflow's own key loses to the manifest: %+v", a)
	}
	if a := draftApproval(cfg, workflow.Workflow{Name: "slack-stalled-nudge"}); a.Mode != draft.Always {
		t.Errorf("a rule the config leaves alone: %+v", a)
	}
}

func TestAnAskOwnerDraftWaitsForAPersonUnderAWindowApproval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	r.te.cfg.Workflows = map[string]config.WorkflowConfig{"slack-ask-owner": {DraftApproval: &config.ProposalApproval{Mode: "window", Window: config.Days(time.Hour)}}}
	p := newWorkflowProposer(r)
	r.scenario(r.now)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	ds, err := r.svc.List(ctx, outbox.Filter{Workflow: "slack-ask-owner"})
	if err != nil || len(ds) == 0 {
		t.Fatalf("ask-owner drafts %+v, %v", ds, err)
	}
	for _, d := range ds {
		if d.State != draft.Proposed || d.Approval.Mode != draft.Always || !d.Deadline.IsZero() {
			t.Errorf("an ask-owner draft goes out on its own: %+v", d)
		}
	}
}

func TestAUserWorkflowThatFailsToLoadKeepsItsDrafts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "workflows", "slack-ask-owner", "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "workflows", "slack-ask-owner", "text.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := userProfile(t, r.te, map[string]string{"workflows/my-ask/workflow.toml": string(b), "workflows/my-ask/text.md.tmpl": string(text)})
	manifest := filepath.Join(dir, "workflows", "my-ask", "workflow.toml")
	off := false
	r.te.cfg.Workflows = map[string]config.WorkflowConfig{"slack-ask-owner": {Enabled: &off}}
	p := newWorkflowProposer(r)
	r.scenario(r.now)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	mine := func() []draft.Draft {
		t.Helper()
		ds, err := r.svc.List(ctx, outbox.Filter{Workflow: "my-ask"})
		if err != nil {
			t.Fatal(err)
		}
		return ds
	}
	if ds := mine(); len(ds) == 0 || ds[0].State != draft.Proposed {
		t.Fatalf("my-ask proposed %+v; broken %v", ds, r.te.loadedIntents().BrokenWorkflows)
	}
	if err := os.WriteFile(manifest, append(b, []byte("\nwhen_typo = true\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, broken := r.te.loadedIntents().BrokenWorkflows["my-ask"]; !broken {
		t.Fatal("the typo did not break my-ask")
	}
	for _, d := range mine() {
		if d.State != draft.Proposed {
			t.Fatalf("a workflow that failed to load lost its draft: %+v", d)
		}
	}
	if err := os.RemoveAll(filepath.Dir(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	for _, d := range mine() {
		if d.State != draft.Discarded || d.Reason != draft.WithdrawMoot {
			t.Fatalf("a removed workflow kept its draft: %+v", d)
		}
	}
}
