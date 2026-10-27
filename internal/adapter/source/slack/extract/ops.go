package extract

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/slack/send"
)

// Ops diffs r against in.Ledger: create, update, assign, resolve, reopen, link. People are
// items.Person{Key: "slack:<id>", Name, Aliases: ["email:<addr>"] when the cache has an email}.
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
	return ops, dropped, nil
}

type opBuilder struct {
	in     Input
	ledger map[string]items.Item
	ids    map[string]string // new ref -> minted id
	taken  map[string]bool
	groups map[string]string
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
	if out.Requester != "" {
		p := b.person(out.Requester)
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

var (
	slackUserID  = regexp.MustCompile(`^[UW][A-Z0-9]+$`)
	slackGroupID = regexp.MustCompile(`^S[A-Z0-9]+$`)
)

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

func (b *opBuilder) people(ids []string) []items.Person {
	out := make([]items.Person, 0, len(ids))
	for _, id := range ids {
		out = append(out, b.person(id))
	}
	return out
}

// person is a Slack user for a user id, else a group: a user group id or a team's name.
func (b *opBuilder) person(id string) items.Person {
	if slackGroupID.MatchString(id) {
		return items.Person{Key: "group:" + id, Name: slack.GroupName(id, b.groupHandles())}
	}
	if !slackUserID.MatchString(id) {
		return items.Person{Key: "group:" + id, Name: id}
	}
	p := items.Person{Key: "slack:" + id, Name: userName(b.in, id)}
	if u, ok := b.in.Users[id]; ok && u.Email != "" {
		p.Aliases = []string{"email:" + u.Email}
	}
	return p
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
