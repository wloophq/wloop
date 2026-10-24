package slack

import (
	"context"
	"fmt"
	"strconv"

	slackrows "github.com/wloophq/wloop/internal/adapter/source/slack/rows"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
)

const (
	toneNeutral card.Tone = "neutral"
	toneWarn    card.Tone = "warn"
	toneInfo    card.Tone = "info"
)

// Card is a Slack unit's card, read from its row alone.
func Card(_ context.Context, r api.Row) (card.Card, error) {
	p, ok := slackrows.PayloadOf(r)
	if !ok {
		return card.Card{}, fmt.Errorf("slack card: %s has no Slack payload", r.Unit.Label())
	}
	c := card.Card{
		Unit:          r.Unit,
		Label:         r.Label,
		Title:         r.Title,
		State:         string(r.Kind),
		Signals:       signals(r, p),
		Why:           r.Summary.Reason,
		WaitsOn:       p.WaitsOn,
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
		c.LastHuman = &card.Human{Who: h.Who, What: h.What, At: h.At, Link: h.Link}
	}
	return c, nil
}

func signals(r api.Row, p slackrows.Payload) []card.Signal {
	open := card.Signal{Key: "items", Value: "none open", Tone: toneNeutral}
	switch n := r.Summary.OpenItems; {
	case n == 1:
		open.Value, open.Tone = "1 open", toneWarn
	case n > 1:
		open.Value, open.Tone = strconv.Itoa(n)+" open", toneWarn
	}
	out := []card.Signal{open, {Key: "replies", Value: strconv.Itoa(p.Replies), Tone: toneNeutral}}
	if !p.LastActivity.IsZero() {
		out = append(out, card.Signal{Key: "last activity", Value: p.LastActivity.UTC().Format("2006-01-02 15:04 UTC"), Tone: toneInfo,
			At: p.LastActivity})
	}
	return out
}
