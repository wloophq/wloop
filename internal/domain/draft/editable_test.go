package draft

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestAPersonChangesOnlyWhatTheKindLetsThem(t *testing.T) {
	t.Parallel()
	review := KindSpec{Provider: "github", Name: "review", Editable: []string{EditableText, "/event"}}
	resolve := KindSpec{Provider: "github", Name: "resolve"}
	before := Draft{Kind: "review", Text: "Two findings.", Payload: json.RawMessage(`{"event":"COMMENT","comments":[{"path":"a.go","body":"x"}]}`)}
	for name, c := range map[string]struct {
		spec KindSpec
		edit func(*Draft)
		ok   bool
	}{
		"the text of a review": {review, func(d *Draft) { d.Text = "One finding." }, true},
		"the event of a review": {review, func(d *Draft) {
			d.Payload = json.RawMessage(`{"event":"APPROVE","comments":[{"path":"a.go","body":"x"}]}`)
		}, true},
		"an inline comment of a review": {review, func(d *Draft) {
			d.Payload = json.RawMessage(`{"event":"COMMENT","comments":[{"path":"a.go","body":"y"}]}`)
		}, false},
		"the text of a kind that locks it": {resolve, func(d *Draft) { d.Text = "anything" }, false},
		"nothing, on a kind that locks it": {resolve, func(*Draft) {}, true},
		"the same payload in other spacing": {resolve, func(d *Draft) {
			d.Payload = json.RawMessage(`{ "event": "COMMENT", "comments": [{"path": "a.go", "body": "x"}] }`)
		}, true},
		"a payload that is not JSON": {review, func(d *Draft) { d.Payload = json.RawMessage(`{"event":`) }, false},
	} {
		after := before
		c.edit(&after)
		err := c.spec.CheckEdit(before, after)
		if (err == nil) != c.ok || err != nil && !errors.Is(err, ErrNotEditable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
