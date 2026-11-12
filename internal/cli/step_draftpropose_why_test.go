package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

var brt = time.FixedZone("BRT", -3*60*60)

// writeProfile writes body to rel under the rig's user profile.
func (r *draftRig) writeProfile(rel, body string) {
	r.t.Helper()
	p := filepath.Join(r.cfg.Profile, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *draftRig) proposed() draft.Draft {
	r.t.Helper()
	r.run(draftThread)
	ds := r.drafts()
	if len(ds) != 1 {
		r.t.Fatalf("%d drafts, want one", len(ds))
	}
	return ds[0]
}

func TestADraftCarriesTheWhyItsWorkflowWrites(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("wf", "purpose", "nudge"), "Any news?")
	r.now = time.Date(2026, 11, 6, 10, 0, 0, 0, brt)
	r.writeProfile("workflows/wf/why.md.tmpl", "No answer since {{.Since.Format \"Jan 2\"}}\n")
	r.items.set(draftThread, openItem("i1", "The deck", time.Date(2026, 11, 5, 1, 30, 0, 0, time.UTC)),
		openItem("i2", "The budget", time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)))
	if why := r.proposed().Why; why != "No answer since Nov 4" {
		t.Errorf("why %q, want the oldest item's day in the user's zone", why)
	}
}

func TestTheWhyIsInTheUsersLocaleAndTheTextInTheConversations(t *testing.T) {
	t.Parallel()
	r := languageRig(t, true, "en", "pt-BR")
	r.writeProfile("workflows/wf/why.md.tmpl", "Waiting on {{len .Items}}")
	r.writeProfile("locales/pt-BR/workflows/wf/why.md.tmpl", "Esperando {{len .Items}}")
	d := r.proposed()
	if d.Text != "Any news?" || d.Language != "en" || d.Why != "Esperando 1" {
		t.Errorf("text %q in %q, why %q", d.Text, d.Language, d.Why)
	}
}

func TestAWorkflowWithNoWhyFileProposesWithAnEmptyWhy(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("wf", "purpose", "nudge"), "Any news?")
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-72*time.Hour)))
	if d := r.proposed(); d.Why != "" || d.Text != "Any news?" {
		t.Errorf("why %q, text %q", d.Why, d.Text)
	}
}

func TestAWhyIsAFileOfTheWorkflowsDirectory(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("wf", "purpose", "nudge", "why", "../x.tmpl"), "Any news?")
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-72*time.Hour)))
	_, err := r.s.Check(context.Background(), r.wf.Steps[0], draftThread, workflow.Why{Workflow: "wf"}, automation.CheckOpts{})
	if err == nil || !strings.Contains(err.Error(), "why") || !strings.Contains(err.Error(), "workflow's directory") {
		t.Fatalf("err %v, want why refused as a file outside the workflow's directory", err)
	}
}

// draftWorkflows are the bundled profile's and plugins' workflows with a draft.propose step, with its delivery.
func draftWorkflows(t *testing.T) map[string]draft.Delivery {
	t.Helper()
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	l, problems := intentload.LoadIn("en", profile.BundledChain(), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatalf("the bundled profile loads with problems: %v", problems)
	}
	out := map[string]draft.Delivery{}
	for _, w := range l.Workflows {
		for _, st := range w.Steps {
			if st.Use == builtin.StepDraftPropose {
				d, _ := st.Args["delivery"].(string)
				out[w.Name] = draft.Delivery(d)
			}
		}
	}
	return out
}

func TestEveryBundledDraftWorkflowHasAWhy(t *testing.T) {
	t.Parallel()
	data := draftTextData{Workflow: "wf", Purpose: "nudge", Unit: draftThread.String(), Since: time.Date(2026, 11, 4, 9, 0, 0, 0, brt),
		Person: draftTextPerson{Key: "slack:UANA", Name: "Ana"}, People: []draftTextPerson{{Key: "slack:UANA", Name: "Ana"}, {Key: "slack:UBRUNO", Name: "Bruno"}},
		Items: []draftTextItem{{ID: "i1", Line: "The deck", Summary: "The deck"}, {ID: "i2", Line: "The budget", Summary: "The budget"}},
		Facts: workflow.Facts{"task.promised": time.Date(2026, 11, 6, 15, 0, 0, 0, brt)}}
	wfs := draftWorkflows(t)
	if len(wfs) == 0 {
		t.Fatal("no bundled workflow proposes a draft")
	}
	for name, delivery := range wfs {
		for _, locale := range []string{"en", "pt-BR"} {
			for _, file := range []string{"why.md.tmpl", defaultDraftText} {
				if file == defaultDraftText && slices.Contains(builtin.DraftEmojiDeliveries, string(delivery)) {
					continue
				}
				tmpl, written, err := draftTemplate(profile.BundledChain(), turn.BundledFS(), name, file, draft.Delivery("message"), locale)
				if err != nil {
					t.Errorf("%s %s in %s: %v", name, file, locale, err)
					continue
				}
				if written != locale {
					t.Errorf("%s %s in %s is written in %s", name, file, locale, written)
				}
				var b bytes.Buffer
				if err := tmpl.Execute(&b, data); err != nil || strings.TrimSpace(b.String()) == "" {
					t.Errorf("%s %s in %s renders %q %v", name, file, locale, b.String(), err)
				}
			}
		}
	}
}

func TestAClosureWordsEachResolutionInItsLanguage(t *testing.T) {
	t.Parallel()
	data := draftTextData{Items: []draftTextItem{{Line: "The deck", Resolution: "answered"}, {Line: "The budget", Resolution: "moot"}, {Line: "The copy"}}}
	for locale, want := range map[string]string{
		"pt-BR": "- The deck: respondido\n- The budget: sem efeito\n- The copy: resolvido",
		"en":    "- The deck: answered\n- The budget: moot\n- The copy: resolved",
	} {
		tmpl, written, err := draftTemplate(profile.BundledChain(), turn.BundledFS(), "slack-closure", defaultDraftText, draft.Delivery("message"), locale)
		if err != nil || written != locale {
			t.Fatalf("%s: written in %q, %v", locale, written, err)
		}
		var b bytes.Buffer
		if err := tmpl.Execute(&b, data); err != nil || !strings.HasSuffix(b.String(), want) {
			t.Errorf("%s: %q %v, want it to end %q", locale, b.String(), err, want)
		}
	}
}
