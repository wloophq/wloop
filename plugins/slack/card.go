package slack

import (
	"fmt"
	"strconv"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/host"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
)

const (
	toneNeutral = "neutral"
	toneWarn    = "warn"
	toneInfo    = "info"
)

// Card is a Slack unit's card, read from its row alone.
func Card(r slackrows.Row) (src.Card, error) {
	p, ok := slackrows.PayloadOf(r)
	if !ok {
		return src.Card{}, fmt.Errorf("slack card: %s has no Slack payload", r.Unit)
	}
	c := src.Card{
		Label:         r.Label,
		Title:         r.Title,
		State:         string(r.Kind),
		Signals:       signals(r, p),
		Why:           r.Summary.Reason,
		WaitsOn:       host.WirePeople(p.WaitsOn),
		Link:          r.Link,
		TurnsLink:     "#unit=" + string(r.Unit.Provider) + "/" + r.Unit.Key,
		StartedByMe:   p.StartedByViewer,
		ClosurePosted: p.ClosurePosted,
		Cares:         p.Followed || p.Direct,
	}
	if r.Summary.OpenItems > 0 {
		c.Since = r.Summary.Since
	}
	if h := p.LastHuman; h != nil {
		c.LastHuman = &src.Human{Who: host.WirePerson(h.Who), What: h.What, At: h.At, Link: h.Link}
	}
	return c, nil
}

// The keys of a card's signals and the codes of their values, which the plugin's ui.toml [signals] words.
const (
	SignalItems        = "items"
	SignalReplies      = "replies"
	SignalLastActivity = "last_activity"

	ItemsNone    = "none"
	ItemsOpen    = "open"
	RepliesCount = "count"
)

func signals(r slackrows.Row, p slackrows.Payload) []src.CardSignal {
	open := src.CardSignal{Key: SignalItems, Value: ItemsNone, Tone: toneNeutral}
	if n := r.Summary.OpenItems; n > 0 {
		open.Value, open.Tone, open.Detail = ItemsOpen, toneWarn, count(n)
	}
	out := []src.CardSignal{open, {Key: SignalReplies, Value: RepliesCount, Tone: toneNeutral, Detail: count(p.Replies)}}
	if !p.LastActivity.IsZero() {
		out = append(out, src.CardSignal{Key: SignalLastActivity, Tone: toneInfo, At: p.LastActivity})
	}
	return out
}

func count(n int) *src.SignalDetail {
	return &src.SignalDetail{Params: map[string]string{"n": strconv.Itoa(n)}}
}
