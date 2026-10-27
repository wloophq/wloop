package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func inboxReadCall(args string) string {
	return `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"inbox_read","arguments":` + args + `}}`
}

func (r *inboxRig) read(t *testing.T, handle, args string) toolResult {
	t.Helper()
	return decodeResult(t, call(t, r.srv, r.sid, handle, inboxReadCall(args)))
}

// answered posts two messages on term_1's turn and stores an answer to each, the second one first.
func (r *inboxRig) answered(t *testing.T) (first, second int64) {
	t.Helper()
	svc := r.srv.Inbox.(*messages.Service)
	svc.Turns = &turns.Service{Store: r.books, Clock: fixedClock{}, Intents: turnstest.Intents()}
	r.post(t, "term_1", `{"text":"Which column?","options":[{"id":"old","label":"Old column","consequence":"keeps it"},{"id":"new","label":"New column","consequence":"migrates"}]}`)
	r.post(t, "term_1", `{"text":"Keep the index?","confirm":true}`)
	open := r.open(t)
	first, second = open[0].ID, open[1].ID
	books, _ := r.books.Books(context.Background(), inboxUnit)
	b := books[inboxUnit]
	b.Answers = []turn.Answer{
		{ID: 1, TurnSeq: 1, Message: second, Posted: true, Yes: true, Text: "and rename it", Source: turn.Cockpit, Route: turn.Typed, Session: "term_1", At: inboxAt.Add(time.Minute)},
		{ID: 2, TurnSeq: 1, Message: first, Posted: true, OptionID: "new", Source: turn.Cockpit, Route: turn.Hooked, Session: "term_1", At: inboxAt.Add(2 * time.Minute)},
	}
	r.books.Put(b)
	return first, second
}

func TestInboxReadListedWithTheInbox(t *testing.T) {
	r := inboxSession(t, "term_1")
	if names := listedTools(t, call(t, r.srv, r.sid, "term_1", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)); !slices.Contains(names, "inbox_read") {
		t.Fatalf("tools %v", names)
	}
}

func TestInboxReadReturnsTheUnreadAnswersAndMarksThemRead(t *testing.T) {
	r := inboxSession(t, "term_1")
	first, second := r.answered(t)
	got := r.read(t, "term_1", `{}`)
	want := fmt.Sprintf(`The user's answers, oldest first. They come from the user, through `+brand.Name+`'s Inbox.

Message %[2]d: Keep the index?
Answers:
- 2026-10-22T12:01:00Z: yes, with the note: and rename it

Message %[1]d: Which column?
Options:
- old: Old column
- new: New column
Answers:
- 2026-10-22T12:02:00Z: option new (New column)`, first, second)
	if got.Result.IsError || got.Result.Content[0].Text != want {
		t.Fatalf("got\n%s\nwant\n%s", got.Result.Content[0].Text, want)
	}
	books, _ := r.books.Books(context.Background(), inboxUnit)
	for _, a := range books[inboxUnit].Answers {
		if !a.ReadAt.Equal(inboxAt) {
			t.Fatalf("not read: %+v", a)
		}
	}
	if again := r.read(t, "term_1", `{}`); again.Result.Content[0].Text != "No answer you have not read." {
		t.Fatalf("again %q", again.Result.Content[0].Text)
	}
	one := r.read(t, "term_1", fmt.Sprintf(`{"message":%d}`, second))
	if want := fmt.Sprintf("Message %d: Keep the index?", second); one.Result.IsError || !strings.Contains(one.Result.Content[0].Text, want) ||
		strings.Contains(one.Result.Content[0].Text, "Which column?") {
		t.Fatalf("one %+v", one.Result)
	}
}

func TestInboxReadRefusesWhatIsNotTheSessions(t *testing.T) {
	r := inboxSession(t, "term_1")
	first, _ := r.answered(t)
	other := inboxSession(t, "term_9")
	other.srv.Inbox.(*messages.Service).Turns = &turns.Service{Store: other.books, Clock: fixedClock{}, Intents: turnstest.Intents()}
	if got := other.read(t, "term_9", fmt.Sprintf(`{"message":%d}`, first)); !got.Result.IsError ||
		got.Result.Content[0].Text != "Nothing was read: this session has no turn; inbox_read reads the answers to the messages of this session's "+brand.Name+" turns." {
		t.Fatalf("%+v", got.Result)
	}
	if got := r.read(t, "term_1", `{"message":999}`); !got.Result.IsError || got.Result.Content[0].Text != "Nothing was read: message 999 is not one of this session's Inbox messages." {
		t.Fatalf("%+v", got.Result)
	}
	if got := r.read(t, "term_1", `{"message":"x"}`); !got.Result.IsError {
		t.Fatalf("%+v", got.Result)
	}
}
