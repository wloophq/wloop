package cli

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// FINAL-A2: outbox edit from an agent's shell that names no session handle passes for the person, though
// outbox approve, retry and new --send refuse that shell.
func TestFinalAttackAgentShellEditsAsThePerson(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Target: "#general", Text: "the person's words", State: draft.Held})
	te.Getenv = func(k string) string {
		if k == "CLAUDE_CODE_SESSION_ID" {
			return "session-1"
		}
		return ""
	}
	code := te.run("outbox", "edit", "1", "injected words", "--rev", "1")
	got := te.draftState(t, db, d.ID)
	if code == 0 || got.Text != "the person's words" {
		t.Errorf("an agent's shell edited the draft as the person: exit %d, stored %q by %+v (%s)", code, got.Text, got.By, strings.TrimSpace(te.err.String()))
	}
}

// FINAL-A2b: outbox discard from the same shell resolves the draft's gate as the person.
func TestFinalAttackAgentShellDiscardsAsThePerson(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	d := insertDraft(t, db, draft.Draft{Unit: threadUnit, Target: "#general", Text: "the person's words", State: draft.Held})
	te.Getenv = func(k string) string {
		if k == "CLAUDE_CODE_SESSION_ID" {
			return "session-1"
		}
		return ""
	}
	code := te.run("outbox", "discard", "1")
	if got := te.draftState(t, db, d.ID); code == 0 || got.State != draft.Held {
		t.Errorf("an agent's shell resolved the draft's gate as the person: exit %d, draft %s (%s)", code, got.State, got.Reason)
	}
}
