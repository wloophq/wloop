package ghsource

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/termfmt"
)

// Cards are the pull requests' cards and items: the ones the GitHub
// plugin reported, under the overlays the host laid on their rows.
type Cards struct{ Reports readmodel.Reports }

var errNoReport = errors.New("the GitHub plugin has reported no such pull request")

func (c Cards) report(ctx context.Context, r api.Row) (prrow.Report, error) {
	if r.Unit.Provider != unit.GitHub {
		return prrow.Report{}, fmt.Errorf("github card: %s is not a pull request", r.Unit.Label())
	}
	if c.Reports == nil {
		return prrow.Report{}, errNoReport
	}
	snaps, err := c.Reports.Reported(ctx, []unit.Unit{r.Unit})
	if err != nil {
		return prrow.Report{}, err
	}
	rep, ok, err := prrow.ReportOf(snaps[r.Unit])
	if err != nil {
		return prrow.Report{}, err
	}
	if !ok {
		return prrow.Report{}, fmt.Errorf("%s: %w", r.Unit.Label(), errNoReport)
	}
	return rep, nil
}

// Card is the card of the pull request of r.
func (c Cards) Card(ctx context.Context, r api.Row) (card.Card, error) {
	s, err := c.report(ctx, r)
	if err != nil {
		return card.Card{}, err
	}
	return CardOf(r, s)
}

// Items are the items of the pull request of r.
func (c Cards) Items(ctx context.Context, r api.Row) ([]items.Item, error) {
	s, err := c.report(ctx, r)
	if errors.Is(err, errNoReport) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ItemsOf(r, s), nil
}

// CardOf is the card the plugin reported in s, under the overlays of r,
// the pull request's row: the row says why, on whom and since when, its
// situation signal words the row's kind, a mute snoozes it, and stalled
// follows the host's clock.
func CardOf(r api.Row, s prrow.Report) (card.Card, error) {
	p, ok := prrow.PayloadOf(r)
	if !ok || s.Card == nil {
		return card.Card{}, fmt.Errorf("github card: %s has no pull request payload", r.Unit.Label())
	}
	rc := s.Card
	c := card.Card{Unit: r.Unit, Label: r.Label, Title: rc.Title, State: rc.State, Outcome: items.Resolution(rc.Outcome),
		Signals: []card.Signal{}, Why: r.Summary.Reason, WaitsOn: people(r.Summary.WaitsOn, p), Since: r.Summary.Since, Link: r.Link,
		TurnsLink: rc.TurnsLink, StartedByMe: rc.StartedByMe, ClosurePosted: rc.ClosurePosted, Cares: prrow.InScope(r)}
	overlaid := string(r.Kind) != s.Situation.Kind
	muted := situation.Kind(r.Kind) == situation.KindMuted
	for _, sig := range rc.Signals {
		switch {
		case sig.Key == signalSituation:
			sig.Value = situation.Kind(r.Kind).Label()
		case sig.Key == signalStalled:
			continue
		case overlaid && !muted && sig.Key != signalChecks:
			continue
		}
		c.Signals = append(c.Signals, card.Signal{Key: sig.Key, Value: sig.Value, Tone: card.Tone(sig.Tone), At: sig.At})
	}
	if r.Summary.Stalled && situation.Kind(r.Kind) != situation.KindDone {
		c.Signals = append(c.Signals, card.Signal{Key: signalStalled, Value: "stalled", Tone: "warn"})
	}
	if h := rc.LastHuman; h != nil {
		c.LastHuman = &card.Human{Who: items.Person{Key: h.Who.Key, Name: h.Who.Name, Aliases: slices.Clone(h.Who.Aliases)}, What: h.What, At: h.At, Link: h.Link}
	}
	if m := p.Mute; m != nil {
		c.Mute = &card.Mute{Until: m.Until, UntilStateChange: m.UntilStateChange}
	}
	return c, nil
}

// The keys of the card signals the host lays its overlays on.
const (
	signalSituation = "situation"
	signalStalled   = "stalled"
	signalChecks    = "checks"
)

// ItemsOf are the items the plugin reported in s, under the overlays of r,
// the pull request's row: a pending round is the user's decision, an agent
// waiting on the user with no question the loop holds asks for an answer,
// and the loop working or stopped asks nothing. A muted row keeps the items
// of its unmuted situation.
func ItemsOf(r api.Row, s prrow.Report) []items.Item {
	p, ok := prrow.PayloadOf(r)
	if !ok || p.Viewer == "" {
		return nil
	}
	kind, muted := situation.Kind(r.Kind), false
	if kind == situation.KindMuted {
		kind, muted = situation.Kind(p.UnmutedKind), true
		if r.Pending != nil {
			kind = situation.KindRoundPending
		}
	}
	reported := func() []items.Item {
		var out []items.Item
		for _, it := range s.Items {
			if it.ID == itemWait && r.Question != nil {
				continue
			}
			out = append(out, psource.ItemOf(it))
		}
		return out
	}
	switch kind {
	case situation.KindDone, situation.KindLoopWorking, situation.KindLoopStopped:
		return nil
	case situation.KindRoundPending:
		if len(p.Reasons) == 0 && !p.Workspace.IsReview {
			return nil
		}
		return []items.Item{roundItem(r, p)}
	case situation.KindLoopWaiting:
		switch {
		case muted:
			return nil
		case s.Situation.Kind == string(kind):
			return reported()
		case r.Question == nil && prrow.NeedsMe(r):
			return []items.Item{waitItem(r, p)}
		}
		return nil
	}
	return reported()
}

// itemWait is the id of the item of an agent waiting on the user.
const itemWait = "gh:wait"

func roundItem(r api.Row, p prrow.Payload) items.Item {
	it := loopItem(r, p, []string{"@decide"})
	it.ID, it.Kind = "gh:round", items.Decision
	if pend := r.Pending; pend != nil {
		it.Deadline = pend.Deadline.UTC().Format(time.RFC3339)
	}
	it.Summary = summary("Decide the pending round on", r)
	it.Origin.Ref = "situation:" + string(situation.KindRoundPending)
	return it
}

// waitItem is an agent that waits on the viewer with no question the loop
// holds, such as a permission prompt; a question is the turn's Inbox
// message.
func waitItem(r api.Row, p prrow.Payload) items.Item {
	it := loopItem(r, p, []string{"@agent"})
	it.ID, it.Kind = itemWait, items.Todo
	it.Summary = summary("Answer the agent on", r)
	return it
}

// loopItem is what the loop asks of the viewer on r.
func loopItem(r api.Row, p prrow.Payload, contexts []string) items.Item {
	viewer := person(p.Viewer)
	return items.Item{Unit: r.Unit, Kind: items.Todo, URLs: []string{r.Link}, Contexts: contexts, Initial: items.InitialInbox,
		Origin: unit.Anchor{Unit: r.Unit, Ref: "situation:" + string(r.Kind), Permalink: r.Link}, State: items.Open,
		Confidence: items.High, Provenance: items.Derived, Requester: items.Person{Key: "system:" + brand.Name, Name: brand.Name},
		Assignees: []items.Person{viewer}, WaitingOn: []items.Person{viewer}, Basis: items.NoBasis}
}

func summary(verb string, r api.Row) string {
	s := verb + " " + r.Label
	if r.Title != "" {
		s += ": " + r.Title
	}
	return termfmt.OneLine(s, items.MaxSummary)
}

// person is a GitHub login, or the group of a "team:<org>/<slug>" request.
func person(login string) items.Person {
	if team, ok := strings.CutPrefix(login, "team:"); ok {
		return items.Person{Key: "group:" + team, Name: team}
	}
	return items.Person{Key: "github:" + login, Name: login}
}

// people reads a row's waiting-on: "me", "author", "nobody" or logins.
func people(waitingOn string, p prrow.Payload) []items.Person {
	switch waitingOn {
	case "", "nobody":
		return nil
	case "me":
		return []items.Person{person(p.Viewer)}
	case "author":
		return []items.Person{person(p.Author)}
	}
	var out []items.Person
	for l := range strings.SplitSeq(waitingOn, ", ") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, person(l))
		}
	}
	return out
}
