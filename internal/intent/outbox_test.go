package intent_test

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/turn"
)

const fakeNudge = `summary = "Nudge a quiet conversation"
provider = "task"
when = 'task.owed_by != "me"'

[[step]]
use = "draft.propose"
purpose = "nudge"

[outbox]
title = "Ask who hasn't answered"
about = "Drafted when a conversation where you asked something goes quiet."
`

const fakeNudgePT = `summary = "Cutucar uma conversa quieta"

[outbox]
title = "Perguntar a quem não respondeu"
about = "Rascunhado quando uma conversa em que você perguntou algo fica quieta."
`

func TestOutboxWordsAreLocalized(t *testing.T) {
	user := map[string]string{
		"workflows/fake-nudge/workflow.toml":               fakeNudge,
		"locales/pt-BR/workflows/fake-nudge/workflow.toml": fakeNudgePT,
	}
	for _, c := range []struct{ locale, title, about string }{
		{"en", "Ask who hasn't answered", "Drafted when a conversation where you asked something goes quiet."},
		{"pt-BR", "Perguntar a quem não respondeu", "Rascunhado quando uma conversa em que você perguntou algo fica quieta."},
	} {
		l, problems := loadIn(t, c.locale, turn.BundledFS(), user)
		i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == "fake-nudge" })
		if i < 0 {
			t.Fatalf("%s: fake-nudge did not load: %v", c.locale, problems)
		}
		if w := l.Workflows[i]; w.Outbox.Title != c.title || w.Outbox.About != c.about {
			t.Errorf("%s: outbox words %q, %q, want %q, %q", c.locale, w.Outbox.Title, w.Outbox.About, c.title, c.about)
		}
		en, _ := loadIn(t, "en", turn.BundledFS(), nil)
		d := l.Gates.Drafts
		if d.ByHand == "" || d.ByHandAbout == "" || d.Purposes["nudge"] == "" || d.States["failed"] == "" {
			t.Errorf("%s: draft words %+v, want the hand group's, purposes' and states' words", c.locale, d)
		}
		if translated := d.ByHand != en.Gates.Drafts.ByHand; translated != (c.locale != "en") {
			t.Errorf("%s: the hand group reads %q, English %q", c.locale, d.ByHand, en.Gates.Drafts.ByHand)
		}
	}
}
