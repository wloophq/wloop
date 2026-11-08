package cli

import (
	"testing"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestTheDMNudgeAlwaysWaitsWhateverTheConfigSays(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	var w workflow.Workflow
	for _, x := range te.loadedIntents().Workflows {
		if x.Name == "slack-stalled-nudge-dm" {
			w = x
		}
	}
	if w.Name == "" || !w.DraftsWait {
		t.Fatalf("slack-stalled-nudge-dm %+v, want its drafts pinned to wait", w)
	}
	cfg := Defaults()
	cfg.Workflows = map[string]config.WorkflowConfig{w.Name: {Gates: &config.WorkflowGates{Approval: &config.ApprovalConfig{
		Mode: string(draft.Send), Rule: []config.ApprovalRuleConfig{{When: `gate.kind == "draft"`, Mode: string(draft.Send)}}}}}}
	p := draftApproval(cfg, w, nil, draft.Approval{Mode: draft.Send})
	if p.Default.Mode != draft.Wait || len(p.Rules) != 0 {
		t.Fatalf("policy %+v, want a fixed wait the config cannot release", p)
	}
}
