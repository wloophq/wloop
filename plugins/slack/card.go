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
		Cares:         r.Summary.OpenItems > 0 || p.Followed,
	}
	if r.Summary.OpenItems > 0 {
		c.Since = r.Summary.Since
	}
	if h := p.LastHuman; h != nil {
		c.LastHuman = &src.Human{Who: host.WirePerson(h.Who), What: h.What, At: h.At, Link: h.Link}
	}
	return c, nil
}

func signals(r slackrows.Row, p slackrows.Payload) []src.CardSignal {
	open := src.CardSignal{Key: "items", Value: "none open", Tone: toneNeutral}
	switch n := r.Summary.OpenItems; {
	case n == 1:
		open.Value, open.Tone = "1 open", toneWarn
	case n > 1:
		open.Value, open.Tone = strconv.Itoa(n)+" open", toneWarn
	}
	out := []src.CardSignal{open, {Key: "replies", Value: strconv.Itoa(p.Replies), Tone: toneNeutral}}
	if !p.LastActivity.IsZero() {
		out = append(out, src.CardSignal{Key: "last activity", Value: p.LastActivity.UTC().Format("2006-01-02 15:04 UTC"), Tone: toneInfo,
			At: p.LastActivity})
	}
	return out
}
