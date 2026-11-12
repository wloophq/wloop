package draft

import (
	"bytes"
	"slices"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Content is what a draft posts and what follows its post: a gate approves a draft's Content at its Rev,
// and a write that changes it is a new revision.
type Content struct {
	Unit unit.Unit
	// Items decide where a draft with no target posts, such as a Slack segment's reply to the message an item
	// started at.
	Items     []string
	Kind      Kind
	Delivery  Delivery
	Target    string
	Text      string
	Reaction  string
	Broadcast Broadcast
	Payload   []byte
	Basis     string
	Effects   []string
}

// Content is what d posts.
func (d Draft) Content() Content {
	return Content{Unit: d.Unit, Items: d.Items, Kind: d.Kind, Delivery: d.Delivery, Target: d.Target, Text: d.Text, Reaction: d.Reaction,
		Broadcast: d.Broadcast, Payload: d.Payload, Basis: d.Basis, Effects: d.Effects}
}

// Equal says whether c and o post the same.
func (c Content) Equal(o Content) bool {
	return c.Unit == o.Unit && slices.Equal(c.Items, o.Items) && c.Kind == o.Kind && c.Delivery == o.Delivery && c.Target == o.Target && c.Text == o.Text &&
		c.Reaction == o.Reaction && c.Broadcast == o.Broadcast && bytes.Equal(c.Payload, o.Payload) && c.Basis == o.Basis &&
		slices.Equal(c.Effects, o.Effects)
}

// SameContent says whether a and b post the same.
func SameContent(a, b Draft) bool { return a.Content().Equal(b.Content()) }
