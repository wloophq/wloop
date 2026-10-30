package extract

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/send"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// itemIDs are the item ids of r's items by ref, as Ops gives them: a ledger
// item's own, a new item's minted.
func itemIDs(in Input, r Result) map[string]string {
	b := opBuilder{in: in, ledger: map[string]items.Item{}, ids: map[string]string{}}
	out := map[string]string{}
	for _, it := range in.Ledger {
		b.ledger[it.ID], out[it.ID] = it, it.ID
	}
	for _, it := range r.Items {
		if isNewRef(it.Ref) {
			out[it.Ref] = b.mint(it.OriginTS)
		}
	}
	return out
}

// Ops diffs r against in.Ledger: create, update, assign, resolve, reopen, link. A person is a
// Slack user ("slack:<id>", with an "email:<addr>" alias when the cache has one), a user group
// ("group:<id>") or a team named in the conversation ("group:<name>"); any other key is refused.
// A new item's id is "e<origin ts without its dot>", suffixed ".2", ".3"... while taken, so a
// retried run creates it once. An item the result omits gets no op.
func Ops(in Input, r Result, team string, parse ParseTarget) (ops []items.Op, dropped int, err error) {
	b := opBuilder{in: in, ledger: map[string]items.Item{}, ids: map[string]string{}}
	for _, it := range in.Ledger {
		b.ledger[it.ID] = it
	}
	for _, out := range r.Items {
		if isNewRef(out.Ref) {
			b.ids[out.Ref] = b.mint(out.OriginTS)
		} else if _, ok := b.ledger[out.Ref]; !ok {
			return nil, 0, fmt.Errorf("extract: item %q is not in the ledger", out.Ref)
		}
	}
	for _, out := range r.Items {
		if id, ok := b.ids[out.Ref]; ok {
			ops = append(ops, b.create(id, out)...)
		} else {
			ops = append(ops, b.change(b.ledger[out.Ref], out)...)
		}
	}
	for _, l := range r.Links {
		to, ok := parse(l.Target)
		if !ok {
			dropped++
			continue
		}
		id := l.Item
		if minted, ok := b.ids[id]; ok {
			id = minted
		} else if _, ok := b.ledger[id]; id != "" && !ok {
			dropped++
			continue
		}
		ops = append(ops, items.Op{Op: "link", Unit: in.Unit, Item: id, Anchor: b.anchor(l.EvidenceTS), To: &to, LinkKind: l.Kind})
	}
	if err := validPeople(ops); err != nil {
		return nil, 0, err
	}
	return ops, dropped, nil
}

type opBuilder struct {
	in     Input
	ledger map[string]items.Item
	ids    map[string]string // new ref -> minted id
	taken  map[string]bool
	groups map[string]string
	names  map[string]string // person key -> the name the ledger holds
}

// ledgerName is the name the ledger holds for key, "" when none worth keeping.
func (b *opBuilder) ledgerName(key, id string) string {
	if b.names == nil {
		b.names = map[string]string{}
		for _, it := range b.ledger {
			for _, p := range peopleOf(it) {
				if p.Name != "" && p.Name != strings.TrimPrefix(strings.TrimPrefix(p.Key, slack.UserKey("")), slack.GroupKey("")) && p.Name != slack.UnknownGroup {
					b.names[p.Key] = p.Name
				}
			}
		}
	}
	return b.names[key]
}

func (b *opBuilder) mint(originTS string) string {
	if b.taken == nil {
		b.taken = map[string]bool{}
		for id := range b.ledger {
			b.taken[id] = true
		}
	}
	if originTS == "" {
		originTS = b.newest()
	}
	base := "e" + strings.ReplaceAll(originTS, ".", "")
	id := base
	for n := 2; b.taken[id]; n++ {
		id = base + "." + strconv.Itoa(n)
	}
	b.taken[id] = true
	return id
}

func (b *opBuilder) newest() string {
	if n := len(b.in.Messages); n > 0 {
		return b.in.Messages[n-1].TS
	}
	return ""
}

// anchor is the message at ts in the conversation, the window's newest message when ts is "".
func (b *opBuilder) anchor(ts string) unit.Anchor {
	if ts == "" {
		ts = b.newest()
	}
	a := unit.Anchor{Unit: b.in.Unit, Ref: send.Ref(b.in.Conversation.ID, ts), At: slack.TSTime(ts)}
	for _, m := range b.in.Messages {
		if m.TS == ts {
			a.Permalink = m.Permalink
			break
		}
	}
	return a
}

func (b *opBuilder) create(id string, out ItemOut) []items.Op {
	f := b.fields(out)
	ops := []items.Op{{Op: "create", Unit: b.in.Unit, Item: id, Anchor: b.anchor(firstOf(out.OriginTS, out.AssignmentTS)), Fields: &f}}
	if len(out.Assignees) > 0 {
		ops = append(ops, b.assign(id, out))
	}
	if out.State == "resolved" {
		ops = append(ops, b.resolve(id, out))
	}
	return ops
}

func (b *opBuilder) change(cur items.Item, out ItemOut) []items.Op {
	var ops []items.Op
	if f, changed := diffFields(cur, b.fields(out)); changed {
		ops = append(ops, items.Op{Op: "update", Unit: b.in.Unit, Item: cur.ID, Anchor: b.anchor(""), Fields: &f})
	}
	if basis := items.Basis(out.AssignmentBasis); basis != cur.Basis || !slices.Equal(keys(cur.Assignees), keys(b.people(out.Assignees))) {
		ops = append(ops, b.assign(cur.ID, out))
	}
	switch {
	case out.State == "resolved" && (cur.State != "resolved" || cur.Resolution != resolution(out)):
		ops = append(ops, b.resolve(cur.ID, out))
	case out.State == "open" && cur.State == "resolved":
		ops = append(ops, items.Op{Op: "reopen", Unit: b.in.Unit, Item: cur.ID, Anchor: b.anchor("")})
	}
	return ops
}

func (b *opBuilder) assign(id string, out ItemOut) items.Op {
	return items.Op{Op: "assign", Unit: b.in.Unit, Item: id, Anchor: b.anchor(firstOf(out.AssignmentTS, out.OriginTS)),
		Assignees: b.people(out.Assignees), Basis: items.Basis(out.AssignmentBasis)}
}

func (b *opBuilder) resolve(id string, out ItemOut) items.Op {
	a := b.anchor(out.EvidenceTS)
	kind := items.EvidenceKind(out.EvidenceKind)
	if kind == "" || kind == "none" {
		kind = "message"
	}
	return items.Op{Op: "resolve", Unit: b.in.Unit, Item: id, Anchor: a, Resolution: resolution(out),
		Evidence: &items.Evidence{Kind: kind, Anchor: a}}
}

func resolution(out ItemOut) items.Resolution {
	if out.Resolution == "none" {
		return ""
	}
	return items.Resolution(out.Resolution)
}

func (b *opBuilder) fields(out ItemOut) items.Fields {
	kind := items.Kind(out.Kind)
	conf := items.Confidence(out.Confidence)
	urls := make([]string, 0, len(out.Links))
	for _, l := range out.Links {
		urls = append(urls, l.URL)
	}
	opts := make([]items.Option, 0, len(out.Options))
	for _, o := range out.Options {
		opts = append(opts, items.Option{Label: o.Option, ArguedBy: b.people(o.BackedBy)})
	}
	waiting := b.people(out.WaitingOn)
	f := items.Fields{
		Kind: &kind, Summary: &out.Summary, Context: &out.Context, Deadline: &out.Deadline,
		Ambiguity: &out.Ambiguity, Options: &opts, URLs: &urls, WaitingOn: &waiting, Confidence: &conf,
	}
	if p := b.person(out.Requester); p.Key != "" {
		f.Requester = &p
	}
	return f
}

// diffFields keeps only the fields of want that differ from cur. On a confirmed item the
// free-text fields are left out: a rewording would become a new proposal on every run.
func diffFields(cur items.Item, want items.Fields) (items.Fields, bool) {
	var f items.Fields
	if *want.Kind != cur.Kind {
		f.Kind = want.Kind
	}
	if !cur.Confirmed && *want.Summary != cur.Summary {
		f.Summary = want.Summary
	}
	if !cur.Confirmed && oneLine(*want.Context) != oneLine(cur.Context) {
		f.Context = want.Context
	}
	if *want.Deadline != cur.Deadline {
		f.Deadline = want.Deadline
	}
	if !cur.Confirmed && *want.Ambiguity != cur.Ambiguity {
		f.Ambiguity = want.Ambiguity
	}
	if !slices.Equal(*want.URLs, cur.URLs) {
		f.URLs = want.URLs
	}
	if !slices.EqualFunc(*want.Options, cur.Options, func(a, b items.Option) bool {
		return a.Label == b.Label && slices.Equal(keys(a.ArguedBy), keys(b.ArguedBy))
	}) {
		f.Options = want.Options
	}
	if want.Requester != nil && want.Requester.Key != cur.Requester.Key {
		f.Requester = want.Requester
	}
	if !slices.Equal(keys(*want.WaitingOn), keys(cur.WaitingOn)) {
		f.WaitingOn = want.WaitingOn
	}
	if !cur.Confirmed && *want.Confidence != cur.Confidence {
		f.Confidence = want.Confidence
	}
	return f, f != items.Fields{}
}

func keys(ps []items.Person) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Key
	}
	return out
}

// groupHandles are the handles of the user groups the input's messages mention.
func (b *opBuilder) groupHandles() map[string]string {
	if b.groups == nil {
		b.groups = map[string]string{}
		for _, m := range append(slices.Clone(b.in.Context), b.in.Messages...) {
			slack.GroupHandles(b.groups, m.Text)
		}
	}
	return b.groups
}

// people are the ledger's people for ps, without the empty ones.
func (b *opBuilder) people(ps []PersonOut) []items.Person {
	out := make([]items.Person, 0, len(ps))
	for _, p := range ps {
		if person := b.person(p); person.Key != "" {
			out = append(out, person)
		}
	}
	return out
}

// person is the ledger's person for p: a Slack user or user group by its id, named from the
// user cache or the messages, else as the ledger names them, else as the answer does; a team
// or role by the name the answer gave; nobody when p is empty.
func (b *opBuilder) person(p PersonOut) items.Person {
	id, name := strings.TrimSpace(p.ID), strings.TrimSpace(p.Name)
	switch {
	case slack.IsUserID(id):
		out := items.Person{Key: slack.UserKey(id), Name: cmp.Or(userName(b.in, id), id)}
		if out.Name == id {
			out.Name = cmp.Or(b.ledgerName(out.Key, id), name, id)
		}
		if u, ok := b.in.Users[id]; ok && u.Email != "" {
			out.Aliases = []string{"email:" + u.Email}
		}
		return out
	case slack.IsGroupID(id):
		out := items.Person{Key: slack.GroupKey(id), Name: slack.GroupName(id, b.groupHandles())}
		if out.Name == slack.UnknownGroup {
			out.Name = cmp.Or(b.ledgerName(out.Key, id), name, slack.UnknownGroup)
		}
		return out
	case name != "":
		return items.Person{Key: slack.TeamKey(name), Name: name}
	}
	return items.Person{}
}

// validPeople refuses an op naming a person the plugin may not write (slack.ValidPersonKey).
func validPeople(ops []items.Op) error {
	for _, op := range ops {
		ps := slices.Clone(op.Assignees)
		if f := op.Fields; f != nil {
			if f.Requester != nil {
				ps = append(ps, *f.Requester)
			}
			if f.WaitingOn != nil {
				ps = append(ps, *f.WaitingOn...)
			}
			if f.Options != nil {
				for _, o := range *f.Options {
					ps = append(ps, o.ArguedBy...)
				}
			}
		}
		for _, p := range ps {
			if err := slack.ValidPersonKey(p.Key); err != nil {
				return fmt.Errorf("extract: item %s: %w", op.Item, err)
			}
		}
	}
	return nil
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
