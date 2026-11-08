package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// renderBundled renders the bundled workflow's file in locale over its, to person.
func renderBundled(t *testing.T, workflowName, file, locale string, person items.Person, its ...items.Item) string {
	t.Helper()
	tmpl, _, err := draftTemplate(profile.Chain(config.Config{}), turn.BundledFS(), workflowName, file, draft.Message, locale)
	if err != nil {
		t.Fatal(err)
	}
	sub := draftSubject{unit: dmWithAna}
	data := draftData(draftTextData{People: []draftTextPerson{{Key: person.Key, Name: person.Name}}}, sub, person, its, time.UTC)
	body, err := renderDraft(tmpl, data)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestANudgeReadsTheAskSaidToItsOwner(t *testing.T) {
	t.Parallel()
	asked := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	study := dmItem("i1", "Bruno to do a study of the queue", asked, bruno)
	study.Ask = "do a study of the queue"
	review := dmItem("i2", "Ana to review the switch Me ran", asked, ana)
	review.Ask = "review the switch I ran and point out what I may have missed"
	for _, c := range []struct {
		workflow, file, locale string
		person                 items.Person
		it                     items.Item
	}{
		{"slack-stalled-nudge-dm", defaultDraftText, "en", bruno, study},
		{"slack-stalled-nudge-dm", defaultDraftText, "pt-BR", bruno, study},
		{"slack-ask-owner", defaultDraftText, "en", items.Person{}, review},
		{"slack-ask-owner", defaultDraftText, "pt-BR", items.Person{}, review},
		{"slack-closure", defaultDraftText, "en", items.Person{}, review},
		{"slack-closure", defaultDraftText, "pt-BR", items.Person{}, review},
		{"slack-stalled-nudge", defaultDraftText, "en", ana, review},
		{"slack-stalled-nudge", defaultDraftText, "pt-BR", ana, review},
	} {
		body := renderBundled(t, c.workflow, c.file, c.locale, c.person, c.it)
		if !strings.Contains(body, c.it.Ask) || strings.Contains(body, c.it.Summary) {
			t.Errorf("%s %s: %q, want the ask %q and not the summary", c.workflow, c.locale, body, c.it.Ask)
		}
	}
}

func TestANudgeOverAnItemWithNoAskReadsItsSummary(t *testing.T) {
	t.Parallel()
	old := dmItem("i1", "Bruno to do a study of the queue", time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC), bruno)
	for _, wf := range []struct{ name, file string }{{"slack-stalled-nudge-dm", defaultDraftText}, {"slack-stalled-nudge", defaultDraftText}} {
		if body := renderBundled(t, wf.name, wf.file, "en", bruno, old); !strings.Contains(body, old.Summary) {
			t.Errorf("%s: %q, want the summary of an item extracted before asks", wf.name, body)
		}
	}
}
