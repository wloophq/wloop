package ghsource

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	return c, nil
}

// The keys of the card signals the host lays its overlays on.
const (
	signalSituation = "situation"
	signalStalled   = "stalled"
	signalChecks    = "checks"
)

// ItemsOf are the items the plugin reported in s, under the overlays of r,
// the pull request's row, every one keyed on the pull request so they are
// the asks of its one task. A pending round asks through its plan gate, so
// it keeps the items of the situation under it, as a muted row keeps those
// of its unmuted one. Where the host's turn overlays a situation the plugin
// did not report, the pull request waits on the loop while it works and on
// the user when it stopped or waits with no question, which its gate asks;
// it asks nothing once done.
func ItemsOf(r api.Row, s prrow.Report) []items.Item {
	p, ok := prrow.PayloadOf(r)
	if !ok || p.Viewer == "" {
		return nil
	}
	kind := situation.Kind(r.Kind)
	if kind == situation.KindMuted {
		kind = situation.Kind(p.UnmutedKind)
	}
	switch {
	case kind == situation.KindDone:
		return nil
	case kind == situation.KindRoundPending, s.Situation.Kind == string(kind):
	case kind == situation.KindLoopWorking:
		return []items.Item{followItem(r, p, items.Person{Key: "system:" + brand.Name, Name: brand.Name})}
	case kind == situation.KindLoopStopped, kind == situation.KindLoopWaiting && r.Question == nil && prrow.NeedsMe(r):
		return []items.Item{followItem(r, p, person(p.Viewer))}
	}
	var out []items.Item
	for _, it := range s.Items {
		out = append(out, psource.ItemOf(it))
	}
	return out
}

// followItem is the pull request's item that asks nothing but waits on on,
// as the plugin's own follow item does.
func followItem(r api.Row, p prrow.Payload, on items.Person) items.Item {
	return items.Item{ID: itemFollow, Unit: r.Unit, Kind: items.Todo, URLs: []string{r.Link}, Contexts: []string{repoContext(p.Repo)},
		Origin: unit.Anchor{Unit: r.Unit, Ref: "situation:" + string(r.Kind), Permalink: r.Link}, State: items.Open,
		Confidence: items.High, Provenance: items.Derived, Requester: person(p.Viewer), Assignees: []items.Person{on},
		WaitingOn: []items.Person{on}, Basis: items.NoBasis, Summary: summary("Follow", r), TaskKey: r.Unit.String()}
}

func repoContext(repo string) string {
	_, name, _ := strings.Cut(repo, "/")
	return "@repo-" + cmp.Or(name, repo)
}

// itemFollow is the id of the item of a followed pull request that asks
// nothing else.
const itemFollow = "gh:follow"

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
