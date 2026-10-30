// Package host is the host's broker as the Slack plugin calls it, in the
// plugin's own types: the items ledger, the agent and the profile layer.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/link"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// Tier names a model tier the host's agent maps to one of its models.
type Tier string

const (
	// TierFast is the cheapest model, for short mechanical calls.
	TierFast Tier = "fast"
	// TierBalanced is the everyday workhorse.
	TierBalanced Tier = "balanced"
	// TierFrontier is the most capable model.
	TierFrontier Tier = "frontier"
)

var Tiers = []Tier{TierFast, TierBalanced, TierFrontier}

func ParseTier(s string) (Tier, error) {
	for _, t := range Tiers {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("unknown model tier %q; use %s, %s or %s", s, TierFast, TierBalanced, TierFrontier)
}

// Ask is a prompt for the host's agent.
type Ask struct {
	Tier   Tier
	Prompt string
	// Schema is the JSON schema the answer matches.
	Schema string
	// MaxCostUSD caps the call; 0 leaves it to the agent.
	MaxCostUSD float64
	// MeterScope is the host's meter scope the call runs under.
	MeterScope string
}

// Answer is the agent's answer and what it cost.
type Answer struct {
	JSON  []byte
	Model string
	// CostUSD is nil when the agent did not report it.
	CostUSD  *float64
	Duration time.Duration
	Tokens   int64
}

var (
	// ErrUnfit is an answer that is not JSON fitting the schema.
	ErrUnfit = src.ErrUnfit
	// ErrOverBudget is a call that ran over its cost cap.
	ErrOverBudget = errors.New("the call ran over its budget")
	// ErrUnavailable is an agent or a host that cannot answer now.
	ErrUnavailable = src.ErrUnavailable
)

// Asker answers headless calls.
type Asker interface {
	Ask(ctx context.Context, a Ask) (Answer, error)
}

// Broker is the host's broker, typed in the plugin's words. Its zero
// interfaces answer ErrUnsupported.
type Broker struct{ Host src.Host }

// Ask asks the host's agent; a call over its cost cap fails with
// ErrOverBudget, which the host says as ResourceExhausted.
func (b Broker) Ask(ctx context.Context, a Ask) (Answer, error) {
	if b.Host.Services == nil {
		return Answer{}, src.ErrUnsupported
	}
	if a.MeterScope == "" {
		a.MeterScope = src.CallOf(ctx).MeterScope
	}
	req := src.AskRequest{Prompt: a.Prompt, Tier: string(a.Tier), MaxCostUSD: a.MaxCostUSD, MeterScope: a.MeterScope}
	if a.Schema != "" {
		req.Schema = json.RawMessage(a.Schema)
	}
	res, err := b.Host.Services.Ask(ctx, req)
	if status.Code(err) == codes.ResourceExhausted {
		return Answer{}, fmt.Errorf("%w: %v", ErrOverBudget, err)
	}
	if err != nil {
		return Answer{}, err
	}
	return Answer{JSON: []byte(res.Text), Model: res.Model, CostUSD: res.CostUSD, Duration: res.Duration, Tokens: res.Tokens}, nil
}

// Profile reads path in the plugin's profile layer.
func (b Broker) Profile(ctx context.Context, path string) ([]byte, error) {
	if b.Host.Services == nil {
		return nil, src.ErrUnsupported
	}
	return b.Host.Services.Profile(ctx, path)
}

// Result is what an Apply refused.
type Result struct {
	Refused []Refused
}

// Refused is an op the ledger refused, and why.
type Refused struct {
	Op     items.Op
	Reason string
}

// Apply applies ops to the host's ledger; the host records them as the
// plugin's, of provenance prov.
func (b Broker) Apply(ctx context.Context, _ items.Provenance, ops []items.Op) (Result, error) {
	if b.Host.Ledger == nil {
		return Result{}, src.ErrUnsupported
	}
	wire := make([]src.ItemOp, len(ops))
	for i, o := range ops {
		wire[i] = WireOp(o)
	}
	res, err := b.Host.Ledger.ApplyItems(ctx, wire)
	if err != nil {
		return Result{}, err
	}
	var out Result
	for i, r := range res {
		if r.Error != "" && i < len(ops) {
			out.Refused = append(out.Refused, Refused{Op: ops[i], Reason: r.Error})
		}
	}
	return out, nil
}

// Items are u's items in the host's ledger.
func (b Broker) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	got, err := b.Load(ctx, []unit.Unit{u})
	return got[u], err
}

// Load are the items of units, by unit.
func (b Broker) Load(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error) {
	return b.query(ctx, src.ItemQuery{Units: wireUnits(units)})
}

// All are the items of every unit of the plugin's, by unit.
func (b Broker) All(ctx context.Context) (map[unit.Unit][]items.Item, error) {
	return b.query(ctx, src.ItemQuery{})
}

// Open are the open items of the plugin's units, by unit; no units reads
// every unit's.
func (b Broker) Open(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error) {
	return b.query(ctx, src.ItemQuery{Units: wireUnits(units), OpenOnly: true})
}

func (b Broker) query(ctx context.Context, q src.ItemQuery) (map[unit.Unit][]items.Item, error) {
	out := map[unit.Unit][]items.Item{}
	if b.Host.Ledger == nil {
		return out, src.ErrUnsupported
	}
	got, err := b.Host.Ledger.QueryItems(ctx, q)
	if err != nil {
		return out, err
	}
	for _, w := range got {
		it := ItemOf(w)
		out[it.Unit] = append(out[it.Unit], it)
	}
	return out, nil
}

func wireUnits(us []unit.Unit) []plugin.Unit {
	out := make([]plugin.Unit, len(us))
	for i, u := range us {
		out[i] = u.Wire()
	}
	return out
}

// Category is the task category of a proposed first state.
func Category(i items.Initial) string {
	switch i {
	case items.InitialNext:
		return "ready"
	case items.InitialInbox:
		return "inbox"
	}
	return ""
}

func initial(category string) items.Initial {
	switch category {
	case "ready":
		return items.InitialNext
	case "inbox":
		return items.InitialInbox
	}
	return ""
}

func WireAnchor(a unit.Anchor) src.Anchor {
	return src.Anchor{Unit: a.Unit.Wire(), Ref: a.Ref, Permalink: a.Permalink, At: a.At}
}

func AnchorOf(a src.Anchor) unit.Anchor {
	return unit.Anchor{Unit: unit.Of(a.Unit), Ref: a.Ref, Permalink: a.Permalink, At: a.At}
}

func WirePerson(p items.Person) src.Person {
	return src.Person{Key: p.Key, Name: p.Name, Aliases: slices.Clone(p.Aliases)}
}

func PersonOf(p src.Person) items.Person {
	return items.Person{Key: p.Key, Name: p.Name, Aliases: slices.Clone(p.Aliases)}
}

func WirePeople(ps []items.Person) []src.Person {
	if ps == nil {
		return nil
	}
	out := make([]src.Person, len(ps))
	for i, p := range ps {
		out[i] = WirePerson(p)
	}
	return out
}

func peopleOf(ps []src.Person) []items.Person {
	if ps == nil {
		return nil
	}
	out := make([]items.Person, len(ps))
	for i, p := range ps {
		out[i] = PersonOf(p)
	}
	return out
}

func wireOptions(os []items.Option) []src.Option {
	if os == nil {
		return nil
	}
	out := make([]src.Option, len(os))
	for i, o := range os {
		out[i] = src.Option{Label: o.Label, ArguedBy: WirePeople(o.ArguedBy)}
	}
	return out
}

func optionsOf(os []src.Option) []items.Option {
	if os == nil {
		return nil
	}
	out := make([]items.Option, len(os))
	for i, o := range os {
		out[i] = items.Option{Label: o.Label, ArguedBy: peopleOf(o.ArguedBy)}
	}
	return out
}

func wireEvidence(e items.Evidence) src.ItemEvidence {
	return src.ItemEvidence{Kind: string(e.Kind), Anchor: WireAnchor(e.Anchor)}
}

func evidenceOf(e src.ItemEvidence) items.Evidence {
	return items.Evidence{Kind: items.EvidenceKind(e.Kind), Anchor: AnchorOf(e.Anchor)}
}

// WireItem is it as the protocol carries it.
func WireItem(it items.Item) src.Item {
	out := src.Item{ID: it.ID, Unit: it.Unit.Wire(), Kind: string(it.Kind), Summary: it.Summary, Context: it.Context,
		Options: wireOptions(it.Options), Deadline: it.Deadline, URLs: slices.Clone(it.URLs), Contexts: slices.Clone(it.Contexts),
		Category: Category(it.Initial), Origin: WireAnchor(it.Origin), Requester: WirePerson(it.Requester),
		Assignees: WirePeople(it.Assignees), AssignedAt: WireAnchor(it.AssignedAt), Basis: string(it.Basis),
		WaitingOn: WirePeople(it.WaitingOn), State: string(it.State), Resolution: string(it.Resolution),
		Evidence: wireEvidence(it.Evidence), Confidence: string(it.Confidence), Ambiguity: it.Ambiguity,
		Provenance: string(it.Provenance), Confirmed: it.Confirmed, UserAt: it.UserAt, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
	for _, e := range it.Trail {
		out.Trail = append(out.Trail, wireEvidence(e))
	}
	return out
}

// ItemOf is the item the protocol carries.
func ItemOf(w src.Item) items.Item {
	out := items.Item{ID: w.ID, Unit: unit.Of(w.Unit), Kind: items.Kind(w.Kind), Summary: w.Summary, Context: w.Context,
		Options: optionsOf(w.Options), Deadline: w.Deadline, URLs: slices.Clone(w.URLs), Contexts: slices.Clone(w.Contexts),
		Initial: initial(w.Category), Origin: AnchorOf(w.Origin), Requester: PersonOf(w.Requester), Assignees: peopleOf(w.Assignees),
		AssignedAt: AnchorOf(w.AssignedAt), Basis: items.Basis(w.Basis), WaitingOn: peopleOf(w.WaitingOn), State: items.State(w.State),
		Resolution: items.Resolution(w.Resolution), Evidence: evidenceOf(w.Evidence), Confidence: items.Confidence(w.Confidence),
		Ambiguity: w.Ambiguity, Provenance: items.Provenance(w.Provenance), Confirmed: w.Confirmed, UserAt: w.UserAt,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
	for _, e := range w.Trail {
		out.Trail = append(out.Trail, evidenceOf(e))
	}
	return out
}

// WireOp is o as the protocol carries it. A link op's To with no provider
// is an address the host resolves with every plugin's codec.
func WireOp(o items.Op) src.ItemOp {
	out := src.ItemOp{Op: string(o.Op), Unit: o.Unit.Wire(), Item: o.Item, Anchor: WireAnchor(o.Anchor), Assignees: WirePeople(o.Assignees),
		Basis: string(o.Basis), Resolution: string(o.Resolution), LinkKind: string(o.LinkKind), Reason: o.Reason}
	if f := o.Fields; f != nil {
		w := &src.ItemFields{Summary: f.Summary, Context: f.Context, Deadline: f.Deadline, Ambiguity: f.Ambiguity, URLs: f.URLs, Contexts: f.Contexts}
		if f.Kind != nil {
			k := string(*f.Kind)
			w.Kind = &k
		}
		if f.Options != nil {
			opts := wireOptions(*f.Options)
			w.Options = &opts
		}
		if f.Initial != nil {
			c := Category(*f.Initial)
			w.Category = &c
		}
		if f.Requester != nil {
			p := WirePerson(*f.Requester)
			w.Requester = &p
		}
		if f.WaitingOn != nil {
			ps := WirePeople(*f.WaitingOn)
			w.WaitingOn = &ps
		}
		if f.Confidence != nil {
			c := string(*f.Confidence)
			w.Confidence = &c
		}
		out.Fields = w
	}
	if o.Evidence != nil {
		e := wireEvidence(*o.Evidence)
		out.Evidence = &e
	}
	if o.To != nil {
		to := o.To.Wire()
		out.To = &to
	}
	return out
}

// OpOf is the op the protocol carries.
func OpOf(w src.ItemOp) items.Op {
	out := items.Op{Op: items.OpKind(w.Op), Unit: unit.Of(w.Unit), Item: w.Item, Anchor: AnchorOf(w.Anchor), Assignees: peopleOf(w.Assignees),
		Basis: items.Basis(w.Basis), Resolution: items.Resolution(w.Resolution), LinkKind: link.Kind(w.LinkKind), Reason: w.Reason}
	if f := w.Fields; f != nil {
		o := &items.Fields{Summary: f.Summary, Context: f.Context, Deadline: f.Deadline, Ambiguity: f.Ambiguity, URLs: f.URLs, Contexts: f.Contexts}
		if f.Kind != nil {
			k := items.Kind(*f.Kind)
			o.Kind = &k
		}
		if f.Options != nil {
			opts := optionsOf(*f.Options)
			o.Options = &opts
		}
		if f.Category != nil {
			i := initial(*f.Category)
			o.Initial = &i
		}
		if f.Requester != nil {
			p := PersonOf(*f.Requester)
			o.Requester = &p
		}
		if f.WaitingOn != nil {
			ps := peopleOf(*f.WaitingOn)
			o.WaitingOn = &ps
		}
		if f.Confidence != nil {
			c := items.Confidence(*f.Confidence)
			o.Confidence = &c
		}
		out.Fields = o
	}
	if w.Evidence != nil {
		e := evidenceOf(*w.Evidence)
		out.Evidence = &e
	}
	if w.To != nil {
		to := unit.Of(*w.To)
		out.To = &to
	}
	return out
}
