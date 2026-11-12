package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/record"
)

// TestARowsPinnedAnswerIsRefused keeps a row's answer that approves a pinned draft from being sent, by digit or
// typed, since the row does not show the draft; its other answers still go.
func TestARowsPinnedAnswerIsRefused(t *testing.T) {
	pinned := func() *Model {
		m := newModel(t)
		m.SetData([]api.Row{asking(api.QuestionView{State: "asked", Kind: "choice", Text: "Which review?",
			Options: []record.Option{{ID: "a", Label: "Comment"}, {ID: "b", Label: "Approve", Draft: &record.DraftRef{ID: 42, Rev: 3}}}})}, api.Health{})
		return m
	}
	m := pinned()
	if c, ok := press(m, Key{Rune: '2'}); ok || m.prompt != nil || !strings.Contains(m.status, "draft #42") {
		t.Fatalf("2 on a pinned option: %+v %v prompt %+v status %q", c, ok, m.prompt, m.status)
	}
	m = pinned()
	if c, ok := press(m, append([]Key{{Rune: 'W'}}, append(typed("2"), Key{Name: KeyEnter})...)...); ok || !strings.Contains(m.status, "draft #42") {
		t.Fatalf("2 typed on a pinned option: %+v %v status %q", c, ok, m.status)
	}
	m = pinned()
	if c, ok := press(m, append([]Key{{Rune: 'W'}}, append(typed("1"), Key{Name: KeyEnter})...)...); !ok || c.Req.OptionID != "a" {
		t.Fatalf("1 typed on an unpinned option: %+v %v", c, ok)
	}
	m = newModel(t)
	m.SetData([]api.Row{asking(api.QuestionView{State: "asked", Kind: "confirm", Text: "Post it?", Draft: &record.DraftRef{ID: 42, Rev: 3}})}, api.Health{})
	if c, ok := press(m, append([]Key{{Rune: 'W'}}, append(typed("y"), Key{Name: KeyEnter})...)...); ok || !strings.Contains(m.status, "draft #42") {
		t.Fatalf("yes to a pinned confirm: %+v %v status %q", c, ok, m.status)
	}
}

// TestATasksPinnedOptionShowsItsDraft shows, under the option that pins a draft, the draft as its card serves
// it, its preview and payload included, and a line when it changed since; the digit then answers it. Without the
// pinned draft in view, the digit is refused.
func TestATasksPinnedOptionShowsItsDraft(t *testing.T) {
	pin := &record.DraftRef{ID: 42, Rev: 3}
	shown := api.Draft{ID: 42, Rev: 4, Kind: "review", Purpose: "review", Text: "Ship it.", Preview: "Approve acme/api#7",
		Fields: []api.DraftField{{Pointer: "/event", Label: "Verdict", Value: "APPROVE", Shown: "Approve"}}}
	task := func(pinned []api.Draft) api.TaskView {
		e := viewTasks()["inbox"][0]
		subject, _ := json.Marshal(api.Message{ID: 4, Kind: "question", Text: "Which window?", Recommend: "late", Pinned: pinned,
			Options: []record.Option{{ID: "early", Label: "Before six"}, {ID: "late", Label: "After ten", Draft: pin}}})
		e.Gates[0].Subject = subject
		return e
	}
	m := viewsModel(t)
	chord(m, "gi")
	press(m, Key{Name: KeyEnter})
	m.SetDetail("e2", task([]api.Draft{shown}), nil)
	screen := screenOf(m, 140, 60)
	for _, want := range []string{"approves draft #42", "Approves draft #42 at rev 3", "now at rev 4", "Approve acme/api#7", "Ship it.", "Verdict", "Approve (APPROVE)"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	if c, ok := press(m, Key{Rune: '2'}); !ok || c.args["option_id"] != "late" {
		t.Fatalf("2 with the draft shown: %+v %v", c, ok)
	}
	m = viewsModel(t)
	chord(m, "gi")
	press(m, Key{Name: KeyEnter})
	m.SetDetail("e2", task(nil), nil)
	if c, ok := press(m, Key{Rune: '2'}); ok || !strings.Contains(m.status, "draft #42") {
		t.Fatalf("2 with the draft not shown: %+v %v status %q", c, ok, m.status)
	}
}
