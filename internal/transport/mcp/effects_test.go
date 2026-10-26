package mcp

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestAnMCPDraftDeclaringAnEffectItsIntentDoesNotListIsRefused(t *testing.T) {
	refused := errors.New("the effect unit.closed is not one the reply intent lists in drafts.allowed_effects")
	d := &fakeDrafts{err: refused}
	s, sid := draftSession(t, &fakeSlack{loggedIn: true}, d, "term_1")
	res := decodeResult(t, call(t, s, sid, "term_1", draftCallBody(`{"text":"Closing.","kind":"close","effects":["unit.closed"]}`)))
	got, isErr := res.Result.Content[0].Text, res.Result.IsError
	if !isErr || !strings.Contains(got, "reply intent") || !strings.Contains(got, "Nothing was drafted") {
		t.Fatalf("slack_draft = %q (error %v)", got, isErr)
	}
	if len(d.calls) != 1 || !slices.Equal(d.calls[0].args.Effects, []string{"unit.closed"}) || d.calls[0].args.Kind != "close" {
		t.Fatalf("calls %+v", d.calls)
	}
}
