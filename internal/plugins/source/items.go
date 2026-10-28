package source

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// rawUnit is the domain unit w names, unchecked.
func rawUnit(w plugin.Unit) unit.Unit {
	return unit.Unit{Provider: unit.ProviderID(w.Provider), Key: w.Key}
}

// Category is the task category of a proposed first state.
func category(i items.Initial) string {
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

func wireAnchor(a unit.Anchor) src.Anchor {
	return src.Anchor{Unit: Wire(a.Unit), Ref: a.Ref, Permalink: a.Permalink, At: a.At}
}

func anchorOf(a src.Anchor) unit.Anchor {
	return unit.Anchor{Unit: rawUnit(a.Unit), Ref: a.Ref, Permalink: a.Permalink, At: a.At}
}

func wirePerson(p items.Person) src.Person {
	return src.Person{Key: p.Key, Name: p.Name, Aliases: slices.Clone(p.Aliases)}
}

func personOf(p src.Person) items.Person {
	return items.Person{Key: p.Key, Name: p.Name, Aliases: slices.Clone(p.Aliases)}
}

func wirePeople(ps []items.Person) []src.Person {
	if ps == nil {
		return nil
	}
	out := make([]src.Person, len(ps))
	for i, p := range ps {
		out[i] = wirePerson(p)
	}
	return out
}

func peopleOf(ps []src.Person) []items.Person {
	if ps == nil {
		return nil
	}
	out := make([]items.Person, len(ps))
	for i, p := range ps {
		out[i] = personOf(p)
	}
	return out
}

func wireOptions(os []items.Option) []src.Option {
	if os == nil {
		return nil
	}
	out := make([]src.Option, len(os))
	for i, o := range os {
		out[i] = src.Option{Label: o.Label, ArguedBy: wirePeople(o.ArguedBy)}
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
	return src.ItemEvidence{Kind: string(e.Kind), Anchor: wireAnchor(e.Anchor)}
}

func evidenceOf(e src.ItemEvidence) items.Evidence {
	return items.Evidence{Kind: items.EvidenceKind(e.Kind), Anchor: anchorOf(e.Anchor)}
}

// WireItem is it as the protocol carries it.
func wireItem(it items.Item) src.Item {
	out := src.Item{ID: it.ID, Unit: Wire(it.Unit), Kind: string(it.Kind), Summary: it.Summary, Context: it.Context,
		Options: wireOptions(it.Options), Deadline: it.Deadline, URLs: slices.Clone(it.URLs), Contexts: slices.Clone(it.Contexts),
		Category: category(it.Initial), Origin: wireAnchor(it.Origin), Requester: wirePerson(it.Requester),
		Assignees: wirePeople(it.Assignees), AssignedAt: wireAnchor(it.AssignedAt), Basis: string(it.Basis),
		WaitingOn: wirePeople(it.WaitingOn), State: string(it.State), Resolution: string(it.Resolution),
		Evidence: wireEvidence(it.Evidence), Confidence: string(it.Confidence), Ambiguity: it.Ambiguity,
		Provenance: string(it.Provenance), Confirmed: it.Confirmed, UserAt: it.UserAt, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
	for _, e := range it.Trail {
		out.Trail = append(out.Trail, wireEvidence(e))
	}
	return out
}

// OpOf is the op the protocol carries.
func opOf(w src.ItemOp) items.Op {
	out := items.Op{Op: items.OpKind(w.Op), Unit: rawUnit(w.Unit), Item: w.Item, Anchor: anchorOf(w.Anchor), Assignees: peopleOf(w.Assignees),
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
			p := personOf(*f.Requester)
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
		to := rawUnit(*w.To)
		out.To = &to
	}
	return out
}
