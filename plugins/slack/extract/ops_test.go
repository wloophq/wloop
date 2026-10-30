package extract

import (
	"reflect"
	"strings"
	"testing"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/link"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

type githubCodec struct{}

func (githubCodec) Provider() unit.ProviderID { return githubProvider }

func (githubCodec) Valid(key string) error {
	if !strings.Contains(key, "/") || !strings.Contains(key, "#") {
		return errBadKey
	}
	return nil
}

type keyError struct{}

func (keyError) Error() string { return "bad key" }

var errBadKey = keyError{}

const githubProvider unit.ProviderID = "github"

func parseTarget(target string) (unit.Unit, bool) {
	provider, key, ok := strings.Cut(target, ":")
	if !ok || unit.ProviderID(provider) != githubProvider || (githubCodec{}).Valid(key) != nil {
		return unit.Unit{}, false
	}
	return unit.Unit{Provider: githubProvider, Key: key}, true
}

func opsOf(ops []items.Op, kind items.OpKind) []items.Op {
	var out []items.Op
	for _, op := range ops {
		if op.Op == kind {
			out = append(out, op)
		}
	}
	return out
}

func item(ref string) ItemOut {
	return ItemOut{Ref: ref, Kind: "request", Summary: "s", State: "open", Resolution: "none", EvidenceKind: "none",
		Confidence: "high", Mode: "none", AssignmentBasis: "none"}
}

func TestOpsCreate(t *testing.T) {
	in := loadInput(t)
	r := extractScripted(t, in, "answer.json")
	ops, dropped, err := Ops(in, r, "T0001", parseTarget)
	if err != nil || dropped != 0 {
		t.Fatalf("err = %v, dropped = %d", err, dropped)
	}
	creates := opsOf(ops, "create")
	if len(creates) != 1 {
		t.Fatalf("creates = %+v", creates)
	}
	c := creates[0]
	wantAnchor := unit.Anchor{Unit: threadUnit, Ref: "C0001/1700007200.000400",
		Permalink: "https://example.com/archives/C0001/p1700007200000400?thread_ts=1700000000.000100", At: slack.TSTime("1700007200.000400")}
	if c.Unit != threadUnit || c.Item == "" || c.Anchor != wantAnchor {
		t.Errorf("create = %+v", c)
	}
	f := c.Fields
	if f == nil || *f.Kind != "request" || *f.Summary != r.Items[2].Summary || *f.Confidence != "high" {
		t.Fatalf("fields = %+v", f)
	}
	if want := (items.Person{Key: "slack:U0000000003", Name: "Cy", Aliases: []string{"email:cy@example.com"}}); !reflect.DeepEqual(*f.Requester, want) {
		t.Errorf("requester = %+v", *f.Requester)
	}
	ann := items.Person{Key: "slack:U0000000001", Name: "Ann", Aliases: []string{"email:ann@example.com"}}
	if !reflect.DeepEqual(*f.WaitingOn, []items.Person{ann}) {
		t.Errorf("waiting on = %+v", *f.WaitingOn)
	}
	var assign *items.Op
	for i, op := range ops {
		if op.Op == "assign" && op.Item == c.Item {
			assign = &ops[i]
		}
	}
	if assign == nil || !reflect.DeepEqual(assign.Assignees, []items.Person{ann}) || assign.Basis != "explicit_mention" || assign.Anchor.Ref != "C0001/1700007200.000400" {
		t.Errorf("assign = %+v", assign)
	}
	for _, op := range ops {
		if op.Anchor.Unit != threadUnit || op.Anchor.Ref == "" {
			t.Errorf("op without anchor: %+v", op)
		}
	}
	again, _, _ := Ops(in, r, "T0001", parseTarget)
	if !reflect.DeepEqual(ops, again) {
		t.Error("Ops is not deterministic")
	}
}

func TestOpsAssignAndResolve(t *testing.T) {
	in := loadInput(t)
	in.Ledger = append(in.Ledger, items.Item{ID: "i2", Unit: threadUnit, Kind: "request", Summary: "s", State: "open",
		Requester: items.Person{Key: "slack:U0000000001", Name: "Ann"}, Assignees: []items.Person{{Key: "slack:U0000000002", Name: "Bob"}},
		Basis: "explicit_mention", Confidence: "high"})
	i2 := item("i2")
	i2.Requester = PersonOut{ID: "U0000000001"}
	i2.Assignees = []PersonOut{{ID: "U0000000003"}}
	i2.AssignmentTS = "1700007200.000400"
	i2.AssignmentBasis = "reassigned"
	i2.State, i2.Resolution, i2.EvidenceTS, i2.EvidenceKind = "resolved", "done", "1700003600.000300", "reaction"
	ops, _, err := Ops(in, Result{Items: []ItemOut{i2}}, "T0001", parseTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || ops[0].Op != "assign" || ops[1].Op != "resolve" {
		t.Fatalf("ops = %+v", ops)
	}
	a := ops[0]
	if a.Item != "i2" || a.Basis != "reassigned" || len(a.Assignees) != 1 || a.Assignees[0].Key != "slack:U0000000003" || a.Anchor.Ref != "C0001/1700007200.000400" {
		t.Errorf("assign = %+v", a)
	}
	res := ops[1]
	ev := unit.Anchor{Unit: threadUnit, Ref: "C0001/1700003600.000300",
		Permalink: "https://example.com/archives/C0001/p1700003600000300?thread_ts=1700000000.000100", At: slack.TSTime("1700003600.000300")}
	if res.Item != "i2" || res.Resolution != "done" || res.Anchor != ev || res.Evidence == nil || *res.Evidence != (items.Evidence{Kind: "reaction", Anchor: ev}) {
		t.Errorf("resolve = %+v", res)
	}

	in.Ledger[2].State, in.Ledger[2].Resolution = "resolved", "done"
	in.Ledger[2].Assignees = []items.Person{{Key: "slack:U0000000003"}}
	in.Ledger[2].Basis = "reassigned"
	i2.State, i2.Resolution, i2.EvidenceTS, i2.EvidenceKind = "open", "none", "", "none"
	ops, _, _ = Ops(in, Result{Items: []ItemOut{i2}}, "T0001", parseTarget)
	if len(ops) != 1 || ops[0].Op != "reopen" || ops[0].Item != "i2" || ops[0].Anchor.Ref != "C0001/1700010800.000500" {
		t.Errorf("reopen ops = %+v", ops)
	}
}

func TestOpsUpdateOnlyChangedFields(t *testing.T) {
	in := loadInput(t)
	i1 := item("i1")
	i1.Summary = "Bob reviews the release notes draft by Friday"
	i1.Requester = PersonOut{ID: "U0000000001"}
	i1.Assignees = []PersonOut{{ID: "U0000000002"}}
	i1.AssignmentBasis = "explicit_mention"
	i1.WaitingOn = []PersonOut{{ID: "U0000000002"}}
	ops, _, err := Ops(in, Result{Items: []ItemOut{i1}}, "T0001", parseTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Op != "update" {
		t.Fatalf("ops = %+v", ops)
	}
	f := ops[0].Fields
	if f.Summary == nil || *f.Summary != i1.Summary || f.Kind != nil || f.Requester != nil || f.WaitingOn != nil || f.Confidence != nil {
		t.Errorf("fields = %+v", f)
	}
	i1.Summary = in.Ledger[0].Summary
	ops, _, _ = Ops(in, Result{Items: []ItemOut{i1}}, "T0001", parseTarget)
	if len(ops) != 0 {
		t.Errorf("an unchanged item gave ops %+v", ops)
	}
}

func TestOmittedItemsStay(t *testing.T) {
	in := loadInput(t)
	ops, dropped, err := Ops(in, Result{}, "T0001", parseTarget)
	if err != nil || dropped != 0 || len(ops) != 0 {
		t.Errorf("ops = %+v, dropped = %d, err = %v", ops, dropped, err)
	}
}

func TestLinkOps(t *testing.T) {
	in := loadInput(t)
	i1 := item("i1")
	i1.Summary, i1.Requester, i1.Assignees, i1.AssignmentBasis = in.Ledger[0].Summary, PersonOut{ID: "U0000000001"}, []PersonOut{{ID: "U0000000002"}}, "explicit_mention"
	i1.WaitingOn = []PersonOut{{ID: "U0000000002"}}
	r := Result{Items: []ItemOut{i1}, Links: []LinkOut{{Item: "i1", Target: "github:acme/widget#527", Kind: link.Review, EvidenceTS: "1700007200.000400"}}}
	ops, dropped, err := Ops(in, r, "T0001", parseTarget)
	if err != nil || dropped != 0 {
		t.Fatalf("err = %v, dropped = %d", err, dropped)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %+v", ops)
	}
	l := ops[0]
	want := unit.Unit{Provider: githubProvider, Key: "acme/widget#527"}
	if l.Op != "link" || l.Unit != threadUnit || l.Item != "i1" || l.To == nil || *l.To != want || l.LinkKind != link.Review || l.Anchor.Ref != "C0001/1700007200.000400" {
		t.Errorf("link = %+v", l)
	}

	full := extractScripted(t, in, "answer.json")
	ops, _, _ = Ops(in, full, "T0001", parseTarget)
	links, creates := opsOf(ops, "link"), opsOf(ops, "create")
	if len(links) != 1 || len(creates) != 1 || links[0].Item != creates[0].Item {
		t.Errorf("a link to a new item does not name its create: links %+v, creates %+v", links, creates)
	}
}

func TestUnparsedLinkIsDropped(t *testing.T) {
	in := loadInput(t)
	r := Result{Links: []LinkOut{
		{Item: "i1", Target: "jira:WID-1", Kind: link.Mention, EvidenceTS: "1700007200.000400"},
		{Item: "i1", Target: "https://example.com/x", Kind: link.Mention},
		{Item: "", Target: "github:acme/widget#9", Kind: link.Mention, EvidenceTS: "1700007200.000400"},
	}}
	ops, dropped, err := Ops(in, r, "T0001", parseTarget)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 2 || len(ops) != 1 || ops[0].Item != "" || ops[0].To.Key != "acme/widget#9" {
		t.Errorf("ops = %+v, dropped = %d", ops, dropped)
	}
}

func TestNewItemIDs(t *testing.T) {
	in := loadInput(t)
	in.Ledger = append(in.Ledger, items.Item{ID: "e1700007200000400", Unit: threadUnit, Kind: "todo", Summary: "x", State: "open"})
	a, b := item("n1"), item("n2")
	a.OriginTS, b.OriginTS = "1700007200.000400", "1700007200.000400"
	ops, _, err := Ops(in, Result{Items: []ItemOut{a, b}}, "T0001", parseTarget)
	if err != nil {
		t.Fatal(err)
	}
	creates := opsOf(ops, "create")
	if len(creates) != 2 || creates[0].Item != "e1700007200000400.2" || creates[1].Item != "e1700007200000400.3" {
		t.Errorf("creates = %+v", creates)
	}
	if _, _, err := Ops(in, Result{Items: []ItemOut{item("i9")}}, "T0001", parseTarget); err == nil {
		t.Error("a ref outside the ledger was accepted")
	}
}

func TestConfirmedRewordingIsNoOp(t *testing.T) {
	in := loadInput(t)
	in.Ledger[1].Context = "Cy owns the call.\nBob asked twice."
	i3 := item("i3")
	i3.Kind, i3.Requester, i3.Assignees, i3.AssignmentBasis = "decision", PersonOut{ID: "U0000000002"}, []PersonOut{{ID: "U0000000003"}}, "explicit_mention"
	i3.Summary = "Should the widget toggle ship on by default?"
	i3.Context = "Cy owns the call. Bob asked twice."
	i3.Confidence = "low"
	ops, _, err := Ops(in, Result{Items: []ItemOut{i3}}, "T0001", parseTarget)
	if err != nil || len(ops) != 0 {
		t.Fatalf("a reworded confirmed item gave ops %+v, err %v", ops, err)
	}
	i3.Deadline = "11/17"
	ops, _, _ = Ops(in, Result{Items: []ItemOut{i3}}, "T0001", parseTarget)
	if len(ops) != 1 || ops[0].Op != "update" || ops[0].Fields.Deadline == nil || ops[0].Fields.Summary != nil || ops[0].Fields.Context != nil {
		t.Errorf("a changed deadline gave ops %+v", ops)
	}

	in.Ledger[0].Context = "Line one.\nLine two."
	i1 := item("i1")
	i1.Summary, i1.Requester, i1.Assignees, i1.AssignmentBasis = in.Ledger[0].Summary, PersonOut{ID: "U0000000001"}, []PersonOut{{ID: "U0000000002"}}, "explicit_mention"
	i1.WaitingOn = []PersonOut{{ID: "U0000000002"}}
	i1.Context = "Line one. Line two."
	ops, _, _ = Ops(in, Result{Items: []ItemOut{i1}}, "T0001", parseTarget)
	if len(ops) != 0 {
		t.Errorf("a context echoed on one line gave ops %+v", ops)
	}
}

func TestAPersonIsAUserAGroupOrATeam(t *testing.T) {
	in := Input{Prompts: testPrompts, Messages: []slack.Message{{TS: "1700000000.000100", Text: "<!subteam^S0000000001|@team-a> please"}},
		Users: map[string]slack.User{"U0000000001": {Name: "ana", Email: "ana@example.com"}}}
	b := opBuilder{in: in}
	for name, c := range map[string]struct {
		in   PersonOut
		want items.Person
	}{
		"a cached user":               {PersonOut{ID: "U0000000001"}, items.Person{Key: "slack:U0000000001", Name: "ana", Aliases: []string{"email:ana@example.com"}}},
		"a cached user, named too":    {PersonOut{ID: "U0000000001", Name: "Ana Maria"}, items.Person{Key: "slack:U0000000001", Name: "ana", Aliases: []string{"email:ana@example.com"}}},
		"an unknown user, named":      {PersonOut{ID: "U0000000002", Name: "bia"}, items.Person{Key: "slack:U0000000002", Name: "bia"}},
		"an unknown user":             {PersonOut{ID: "U0000000002"}, items.Person{Key: "slack:U0000000002", Name: "U0000000002"}},
		"a mentioned group":           {PersonOut{ID: "S0000000001", Name: "@whatever"}, items.Person{Key: "group:S0000000001", Name: "@team-a"}},
		"an unmentioned group":        {PersonOut{ID: "S0000000002"}, items.Person{Key: "group:S0000000002", Name: slack.UnknownGroup}},
		"an unmentioned group, named": {PersonOut{ID: "S0000000002", Name: "@team-b"}, items.Person{Key: "group:S0000000002", Name: "@team-b"}},
		"a team by name":              {PersonOut{Name: " Plataforma Billing "}, items.Person{Key: "group:Plataforma Billing", Name: "Plataforma Billing"}},
		"nobody":                      {PersonOut{}, items.Person{}},
	} {
		if got := b.person(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: person(%+v) = %+v, want %+v", name, c.in, got, c.want)
		}
	}
	if got := b.people([]PersonOut{{}, {ID: "U0000000002", Name: "bia"}, {}}); len(got) != 1 || got[0].Key != "slack:U0000000002" {
		t.Errorf("people() = %+v, want bia alone", got)
	}
}

func TestALedgerPersonOutsideTheWindowKeepsTheirName(t *testing.T) {
	in := Input{Prompts: testPrompts, Ledger: []items.Item{{ID: "i1",
		Assignees: []items.Person{{Key: "slack:U0000000099", Name: "Zed"}, {Key: "group:S0000000009", Name: "@team-z"}, {Key: "slack:U0000000098", Name: "U0000000098"}}}}}
	b := opBuilder{in: in, ledger: map[string]items.Item{"i1": in.Ledger[0]}}
	got := b.people([]PersonOut{{ID: "U0000000099"}, {ID: "S0000000009"}, {ID: "U0000000098"}, {ID: "U0000000097", Name: "new"}})
	want := []items.Person{{Key: "slack:U0000000099", Name: "Zed"}, {Key: "group:S0000000009", Name: "@team-z"}, {Key: "slack:U0000000098", Name: "U0000000098"}, {Key: "slack:U0000000097", Name: "new"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("people = %+v, want %+v", got, want)
	}
}

func TestAnAnswerNamingAPersonThePluginMayNotWriteIsRefused(t *testing.T) {
	in := loadInput(t)
	for name, person := range map[string]PersonOut{
		"a rendered user as a team name": {Name: "ana (U0000000001)"},
		"a key as a team name":           {Name: "group:Plataforma Billing"},
		"a mention as a team name":       {Name: "<@U0000000001|ana>"},
	} {
		r := extractScripted(t, in, "answer.json")
		r.Items[2].WaitingOn = []PersonOut{person}
		if _, _, err := Ops(in, r, "T0001", parseTarget); err == nil || !strings.Contains(err.Error(), "person key") {
			t.Errorf("%s: err = %v, want the key refused", name, err)
		}
	}
}
