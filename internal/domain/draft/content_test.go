package draft

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestSameContentComparesWhatADraftPosts(t *testing.T) {
	t.Parallel()
	base := Draft{ID: 7, Unit: unit.Unit{Provider: "github", Key: "acme/api#7"}, Kind: "review", Delivery: "review", Target: "comment:1",
		Text: "Two findings.", Reaction: "", Broadcast: BroadcastOff, Payload: json.RawMessage(`{"event":"COMMENT"}`), Basis: "c0ffee1",
		Items: []string{"i1"}, Effects: []string{"pr.reviewed"}, State: Held, Rev: 1, Version: 1}
	for name, change := range map[string]func(*Draft){
		"unit":      func(d *Draft) { d.Unit.Key = "acme/api#8" },
		"items":     func(d *Draft) { d.Items = []string{"i2"} },
		"kind":      func(d *Draft) { d.Kind = "comment" },
		"delivery":  func(d *Draft) { d.Delivery = "comment" },
		"target":    func(d *Draft) { d.Target = "comment:2" },
		"text":      func(d *Draft) { d.Text = "One finding." },
		"reaction":  func(d *Draft) { d.Reaction = "eyes" },
		"broadcast": func(d *Draft) { d.Broadcast = BroadcastOn },
		"payload":   func(d *Draft) { d.Payload = json.RawMessage(`{"event":"APPROVE"}`) },
		"basis":     func(d *Draft) { d.Basis = "beef002" },
		"effects":   func(d *Draft) { d.Effects = nil },
	} {
		d := base
		d.Effects, d.Items = append([]string(nil), base.Effects...), append([]string(nil), base.Items...)
		change(&d)
		if SameContent(base, d) {
			t.Errorf("a change of its %s posts the same", name)
		}
	}
	for name, change := range map[string]func(*Draft){
		"state":   func(d *Draft) { d.State = Queued },
		"task":    func(d *Draft) { d.Task = "t-1" },
		"rev":     func(d *Draft) { d.Rev, d.Version = 2, 3 },
		"error":   func(d *Draft) { d.Error = "boom" },
		"updated": func(d *Draft) { d.UpdatedAt = time.Unix(1, 0) },
		"hold":    func(d *Draft) { d.Hold = true },
		"author":  func(d *Draft) { d.By = Actor{Kind: ActorPerson} },
	} {
		d := base
		change(&d)
		if !SameContent(base, d) {
			t.Errorf("a change of its %s posts something else", name)
		}
	}
}
