package intent

import (
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
)

func loadIntentWith(t *testing.T, extra string) (*Loaded, Problems) {
	t.Helper()
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal+extra, "Ask.\n")
	return Load(userLayer(root), nil, testBase(), nil)
}

func loadWorkflowWith(t *testing.T, extra string) (*Loaded, Problems) {
	t.Helper()
	root := t.TempDir()
	writeWorkflow(t, root, "close-quiet", quietSession+extra)
	return Load(userLayer(root), nil, testBase(), nil)
}

func TestManifestDraftApproval(t *testing.T) {
	l, problems := loadIntentWith(t, "\n[draft_approval]\nmode = \"window\"\nwindow = \"10m\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	if d.DraftApproval == nil || *d.DraftApproval != (draft.Approval{Mode: draft.Window, Window: 10 * time.Minute}) {
		t.Fatalf("intent: %+v", d.DraftApproval)
	}

	l, problems = loadWorkflowWith(t, "\n[draft_approval]\nmode = \"none\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if w := l.Workflows[0]; w.DraftApproval == nil || *w.DraftApproval != (draft.Approval{Mode: draft.None}) {
		t.Fatalf("workflow: %+v", w.DraftApproval)
	}

	l, _ = loadIntentWith(t, "")
	if d, _ := l.Registry.Get("ask-author"); d.DraftApproval != nil {
		t.Fatalf("intent without a block: %+v", d.DraftApproval)
	}
	l, _ = loadWorkflowWith(t, "")
	if w := l.Workflows[0]; w.DraftApproval != nil {
		t.Fatalf("workflow without a block: %+v", w.DraftApproval)
	}
}

func TestManifestDraftApprovalRefusals(t *testing.T) {
	for _, c := range []struct{ name, block, field, msg string }{
		{"unknown mode", "mode = \"later\"", "draft_approval.mode", "not one of"},
		{"no mode", "window = \"5m\"", "draft_approval.mode", "is required"},
		{"window without a window", "mode = \"window\"", "draft_approval.window", "is required with mode window"},
		{"a window on another mode", "mode = \"none\"\nwindow = \"5m\"", "draft_approval.window", "only with mode window"},
		{"a zero window", "mode = \"window\"\nwindow = \"0s\"", "draft_approval.window", "longer than zero"},
		{"a bad duration", "mode = \"window\"\nwindow = \"soon\"", "draft_approval.window", "not a duration"},
		{"an unknown key", "mode = \"none\"\nafter = \"5m\"", "draft_approval.after", "unknown key"},
	} {
		block := "\n[draft_approval]\n" + c.block + "\n"
		for loader, load := range map[string]func(*testing.T, string) (*Loaded, Problems){"intent": loadIntentWith, "workflow": loadWorkflowWith} {
			t.Run(loader+"/"+c.name, func(t *testing.T) {
				l, problems := load(t, block)
				if len(l.Workflows) > 0 || l.Registry != nil && func() bool { _, ok := l.Registry.Get("ask-author"); return ok }() {
					t.Errorf("the manifest loaded")
				}
				for _, p := range problems {
					if p.Field == c.field && strings.Contains(p.Msg, c.msg) {
						return
					}
				}
				t.Fatalf("want %s: %s, got\n%s", c.field, c.msg, problems.Error())
			})
		}
	}
}
