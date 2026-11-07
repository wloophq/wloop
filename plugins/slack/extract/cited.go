package extract

import (
	"cmp"
	"context"
	"errors"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/send"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// Bounds of what one extraction reads of the units its window cites.
const (
	maxCitedUnits = 5
	maxCitedItems = 20
)

// cited are the open items of the first maxCitedUnits Slack units in's window links to, other
// than own, at most maxCitedItems. An item whose id the ledger or an earlier cited item
// holds, or that reads as a new ref, is left out: an answer could not tell them apart.
func (j *Job) cited(ctx context.Context, team string, in Input, own []unit.Unit) ([]items.Item, error) {
	seen := map[unit.Unit]bool{}
	for _, u := range own {
		seen[u] = true
	}
	taken := map[string]bool{}
	for _, it := range in.Ledger {
		taken[it.ID] = true
	}
	var out []items.Item
	units := 0
	convs := map[string]*slack.Conversation{}
	for _, m := range in.Messages {
		for _, l := range m.Links {
			if units == maxCitedUnits || len(out) == maxCitedItems {
				return out, nil
			}
			msg, ok := send.ParseMessage(l)
			if !ok {
				continue
			}
			u, ok, err := j.unitOf(ctx, team, msg, convs)
			if err != nil {
				return nil, err
			}
			if !ok || seen[u] {
				continue
			}
			seen[u] = true
			units++
			its, err := j.Ledger.Items(ctx, u)
			if err != nil {
				return nil, err
			}
			for _, it := range its {
				if it.State != items.Open || taken[it.ID] || isNewRef(it.ID) || len(out) == maxCitedItems {
					continue
				}
				taken[it.ID] = true
				out = append(out, it)
			}
		}
	}
	return out, nil
}

// unitOf is the unit of the message msg names: its thread's in a channel, its segment's in a
// DM; false when the conversation is unknown or no segment covers it yet. convs caches each
// conversation read, nil for an unknown one.
func (j *Job) unitOf(ctx context.Context, team string, msg send.Message, convs map[string]*slack.Conversation) (unit.Unit, bool, error) {
	conv, ok := convs[msg.Conversation]
	if !ok {
		c, err := j.Facts.Conversation(ctx, msg.Conversation)
		switch {
		case err == nil:
			conv = &c
		case !errors.Is(err, slack.ErrUnknownUnit):
			return unit.Unit{}, false, err
		}
		convs[msg.Conversation] = conv
	}
	if conv == nil {
		return unit.Unit{}, false, nil
	}
	anchor := cmp.Or(msg.ThreadTS, msg.TS)
	if !conv.Kind.DM() {
		return slack.ThreadUnit(team, msg.Conversation, anchor), true, nil
	}
	if j.Segments == nil {
		return unit.Unit{}, false, nil
	}
	us, err := j.Segments.UnitsFor(ctx, msg.Conversation, anchor)
	if err != nil || len(us) == 0 {
		return unit.Unit{}, false, err
	}
	return us[0], true, nil
}
