package profile

import (
	"slices"
	"strings"
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

var todaysFacts = map[Dim]string{
	DimOrganization: "pr.owner", DimRepo: "pr.repo", DimUser: "pr.author", DimTeam: "pr.requested_teams", DimAssociation: "pr.association",
	DimLabel: "pr.labels", DimBase: "pr.base", DimRound: "turn.round", DimReviewer: "pr.address.reviewers",
	DimKind: "brief.kind", DimSize: "brief.size", DimRisk: "brief.risk",
}

func TestTodaysDimsResolveThroughFacts(t *testing.T) {
	if len(Dims) != len(todaysFacts) {
		t.Fatalf("%d dims, %d expected", len(Dims), len(todaysFacts))
	}
	for i, d := range Dims {
		a := Aliases[i]
		if todaysFacts[d] != a.Fact {
			t.Errorf("%s reads %s, want %s", d, a.Fact, todaysFacts[d])
		}
		f, ok := factOf(a.Fact)
		if a.Dim != d || !ok || !f.Dim {
			t.Errorf("%s: alias %+v, fact %s declared %v, flagged dim %v", d, a, a.Fact, ok, f.Dim)
			continue
		}
		def, _ := lookup(Vocabulary(nil), d)
		v := "x"
		if vals, closed := def.closedFor([]unit.ProviderID{unit.GitHub}); closed {
			v = vals[len(vals)-1]
		}
		var value any = v
		if f.Type == expr.List {
			value = []string{v}
		}
		rel := string(d) + "=" + v + "/intent=address.md"
		if d == DimRound {
			rel = string(d) + "=" + v + "/intent=review.md"
		}
		if d == DimRepo {
			rel = "organization=acme/" + rel
		}
		kind := Address
		if d == DimRound {
			kind = Review
		}
		tr := Tree{Blocks: []Block{block(bundled(), rel, "HIT", FrontMatter{})}}
		facts := Facts{Values: map[string]any{"pr.owner": "acme"}, HasBrief: true}
		facts.Values[a.Fact] = value
		if g := tr.Compose(kind, facts); texts(g) != "HIT" {
			t.Errorf("%s: a block on %s does not apply when %s holds it: %+v", d, rel, a.Fact, g)
		}
		facts.Values[a.Fact] = "other"
		if g := tr.Compose(kind, facts); len(g.Parts) != 0 {
			t.Errorf("%s: applies when %s holds another value", d, a.Fact)
		}
	}
}

func TestKindTakesItsProvidersTaxonomy(t *testing.T) {
	if _, ok, err := ParsePath("kind=request/intent=triage.md", chatIntents); err != nil || !ok {
		t.Fatalf("a chat intent's kind is open: %v %v", ok, err)
	}
	if _, _, err := ParsePath("kind=request/intent=review.md", chatIntents); err == nil || !strings.Contains(err.Error(), "kind is one of feature, fix") {
		t.Fatalf("a github intent's kind is github's taxonomy: %v", err)
	}
}

func factOf(name string) (builtin.Fact, bool) {
	i := slices.IndexFunc(builtin.Facts, func(f builtin.Fact) bool { return f.Name == name })
	if i < 0 {
		return builtin.Fact{}, false
	}
	return builtin.Facts[i], true
}

// withDimFacts adds facts to the vocabulary for one test; profile's tests
// that read it do not run in parallel.
func withDimFacts(t *testing.T, fs ...builtin.Fact) {
	t.Helper()
	old := builtin.Facts
	builtin.Facts = append(slices.Clone(old), fs...)
	t.Cleanup(func() { builtin.Facts = old })
}

// chat is an invented provider.
const chat unit.ProviderID = "chat"

var chatIntents = dintent.NewRegistry([]dintent.Def{
	{Name: "review", Profile: "review", Dims: []string{"round"}, Provider: unit.GitHub},
	{Name: "triage", Profile: "triage", Provider: chat},
})

func TestAFactFlaggedDimIsABlockPathCondition(t *testing.T) {
	withDimFacts(t,
		builtin.Fact{Name: "chat.channel", Type: expr.String, Slots: []builtin.FactSlot{builtin.TurnSlot}, Provider: chat, Dim: true},
		builtin.Fact{Name: "turn.trigger", Type: expr.String, Slots: []builtin.FactSlot{builtin.TurnSlot}, Dim: true})
	p, ok, err := ParsePath("organization=acme/turn.trigger=automated/intent=review.md", chatIntents)
	if err != nil || !ok || !slices.Equal(p.Conds, []Cond{{DimOrganization, "acme"}, {"turn.trigger", "automated"}}) {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
	if _, _, err := ParsePath("turn.trigger=automated/organization=acme/intent=review.md", chatIntents); err == nil ||
		!strings.Contains(err.Error(), "rename it to organization=acme/turn.trigger=automated/intent=review.md") {
		t.Fatalf("a fact dim ranks after today's: %v", err)
	}
	if _, ok, err := ParsePath("chat.channel=c-ops/intent=triage.md", chatIntents); err != nil || !ok {
		t.Fatalf("a chat fact under a slack intent: %v %v", ok, err)
	}
	if _, _, err := ParsePath("chat.channel=c-ops/intent=review.md", chatIntents); err == nil ||
		!strings.Contains(err.Error(), "chat.channel does not apply to review: it reads chat.channel, which only chat units have") {
		t.Fatalf("a chat fact under a github intent: %v", err)
	}
	if _, _, err := ParsePath("organization=acme/intent=triage.md", chatIntents); err == nil ||
		!strings.Contains(err.Error(), "organization does not apply to triage: it reads pr.owner, which only github units have") {
		t.Fatalf("a github alias under a slack intent: %v", err)
	}
	if _, _, err := ParsePath("chat.topic=x/intent=triage.md", chatIntents); err == nil || !strings.Contains(err.Error(), "unknown dimension chat.topic") {
		t.Fatalf("an undeclared fact: %v", err)
	}
	tr := Tree{Blocks: []Block{
		blockIn(chatIntents, bundled(), "chat.channel=c-ops/intent=triage.md", "OPS"),
		blockIn(chatIntents, bundled(), "chat.channel=c-web/intent=triage.md", "WEB"),
	}}
	if g := tr.Compose("triage", Facts{Values: map[string]any{"chat.channel": "C-Ops"}}); texts(g) != "OPS" || g.Parts[0].Label != "Chat.channel c-ops ("+bundledName()+")" {
		t.Fatalf("%q %+v", texts(g), g.Parts)
	}
}

func bundledName() string {
	return strings.TrimSuffix(strings.TrimPrefix(bundledLabelSuffix(), " ("), ")")
}

func bundledLabelSuffix() string {
	tr := Tree{Blocks: []Block{block(bundled(), "organization=a/intent=review.md", "", FrontMatter{})}}
	l := tr.Compose(Review, Facts{Values: map[string]any{"pr.owner": "a"}}).Parts[0].Label
	return strings.TrimPrefix(l, "Organization a")
}

func blockIn(intents *dintent.Registry, l Layer, rel, text string) Block {
	p, ok, err := ParsePath(rel, intents)
	if err != nil || !ok {
		panic(rel + ": " + err.Error())
	}
	return Block{Layer: l, File: fileName(l, rel), Path: p, Text: text}
}

func TestSegLabelsComeFromTheProfile(t *testing.T) {
	tr, _ := load(t, map[string]string{
		LabelsFile:                           "[dims]\norganization = \"Org {value}\"\n\n[values]\n\"round=first\" = \"Primeira rodada\"\n",
		"organization=acme/intent=review.md": "ORG",
		"round=first/intent=review.md":       "FIRST",
		"base=main/intent=review.md":         "BASE",
		"tags/change=refac/intent=review.md": "TAG",
	})
	g := tr.Compose(Review, Facts{Values: map[string]any{"pr.owner": "acme", "turn.round": "first", "pr.base": "main"}, HasBrief: true, Tags: []string{"change:refac"}})
	got := labels(g)[1:]
	if want := []string{"Org acme", "Base branch main", "Primeira rodada", "Tag change=refac"}; !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
	if _, err := Load([]Layer{bundled(), user(dir(t, map[string]string{LabelsFile: "colour = \"x\"\n"}))}, testIntents, nil); err == nil ||
		!strings.Contains(err.Error(), LabelsFile) || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("an unknown key: %v", err)
	}
}

func TestTheBundledLabelsNameEveryAlias(t *testing.T) {
	l := bundledLabels()
	for _, d := range Dims {
		def, _ := lookup(Vocabulary(nil), d)
		if l.Dims[string(d)] == "" && !slices.ContainsFunc(def.Closed[""], func(v string) bool { return l.Values[string(d)+"="+v] != "" }) {
			t.Errorf("%s has no label", d)
		}
	}
	if l.Other == "" || l.Tag == "" {
		t.Errorf("%+v", l)
	}
}

var briefIntents = dintent.NewRegistry([]dintent.Def{
	{Name: "review", Profile: "review", Dims: []string{"round"}, Provider: unit.GitHub},
	{Name: "triage", Profile: "triage", Provider: chat},
	{Name: "brief", Builds: "brief", Provider: unit.GitHub, Extra: []jsonschema.Property{
		{Name: "solution", Type: "string"},
		{Name: "approach", Type: "string", Enum: []string{"incremental", "rewrite"}},
		{Name: "areas", Type: "array", Enum: []string{"api", "ui"}},
	}},
})

func TestAnExtraEnumIsABlockPathAndAFacet(t *testing.T) {
	p, ok, err := ParsePath("approach=rewrite/intent=review.md", briefIntents)
	if err != nil || !ok || !slices.Equal(p.Conds, []Cond{{"approach", "rewrite"}}) {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
	for rel, want := range map[string]string{
		"approach=bigbang/intent=review.md": "approach=bigbang is not a valid value; approach is one of incremental, rewrite",
		"solution=x/intent=review.md":       "unknown dimension solution",
		"approach=rewrite/intent=triage.md": "approach does not apply to triage: it reads brief.extra.approach, which only github units have",
	} {
		if _, _, err := ParsePath(rel, briefIntents); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", rel, err, want)
		}
	}
	var facet []DimDef
	for _, d := range Vocabulary(briefIntents) {
		if d.Extra != "" {
			facet = append(facet, d)
		}
	}
	if len(facet) != 2 || facet[0].Name != "approach" || !slices.Equal(facet[0].Closed[unit.GitHub], []string{"incremental", "rewrite"}) || facet[0].List ||
		facet[1].Name != "areas" || !facet[1].List {
		t.Fatalf("the extras a facet reads: %+v", facet)
	}
	tr := Tree{Dims: Vocabulary(briefIntents), Blocks: []Block{
		blockIn(briefIntents, bundled(), "approach=rewrite/intent=review.md", "REWRITE"),
		blockIn(briefIntents, bundled(), "areas=ui/intent=review.md", "UI"),
	}}
	g := tr.Compose(Review, Facts{Values: map[string]any{"brief.extra.approach": "rewrite", "brief.extra.areas": []any{"api", "ui"}}, HasBrief: true})
	if texts(g) != "REWRITE|UI" {
		t.Fatalf("%q", texts(g))
	}
	if g := tr.Compose(Review, Facts{}); len(g.Parts) != 0 || len(g.Skipped) != 2 {
		t.Fatalf("without a brief: %+v", g)
	}
}
