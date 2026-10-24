package turn

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"text/template"

	"github.com/wloophq/wloop/internal/core/instructions"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

type threadPayload struct{ key string }

func (p threadPayload) Unit() unit.Unit { return unit.Unit{Provider: "thread", Key: p.key} }
func (p threadPayload) Arg() string     { return "thread:" + p.key }
func (p threadPayload) Wording() Wording {
	return Wording{Noun: "Slack thread", This: "this Slack thread", Ref: "#eng thread " + p.key, Short: "#eng " + p.key}
}
func (p threadPayload) Fact(name string) (any, bool) {
	if name == "thread.key" {
		return p.key, true
	}
	return nil, false
}

type threadReads struct{ calls int }

func (r *threadReads) Payload(_ context.Context, req Request) (Payload, error) {
	r.calls++
	return threadPayload{req.Unit.Key}, nil
}

var thread1 = unit.Unit{Provider: "thread", Key: "1700000000.1"}

const threadIntent = `label = "Discuss"
summary = "Answer in a thread"
workspace = "given"
provider = "thread"
session = { strategy = "new", title = "discuss" }

[headline]
resumed = "discussion of {{.Wording.Ref}}"
[[headline.new]]
text = "Discuss {{.Wording.Ref}}, {{.Who}}."

[record]
states = ["done", "asked", "failed"]
`

const threadBody = `# {{.Headline}}
{{if .Resume}}{{template "resume" .}}{{end}}
{{template "history" .}}
{{template "record" .}}{{template "brief" .}}`

func threadIntents(t *testing.T) func() *intentload.Loaded {
	t.Helper()
	l, problems := userIntent(t, "discuss", threadIntent, threadBody)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return func() *intentload.Loaded { return l }
}

func TestGitHubContextKeepsThePRFields(t *testing.T) {
	prior := &PriorReview{Verdict: "APPROVED", Head: "9f8"}
	c, err := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{pr: PR{Title: "t", Prior: prior}}), Guides: guides{}}.
		Build(context.Background(), Request{Intent: Intent(dintent.Review), Unit: ref7.Unit()})
	if err != nil {
		t.Fatal(err)
	}
	if c.Unit != ref7.Unit() || c.PR() == nil || c.Payload != Payload(c.PR()) || c.Wording.Noun != "pull request" || c.Wording.Ref != "acme/api#7" {
		t.Fatalf("unit %v payload %T wording %+v", c.Unit, c.Payload, c.Wording)
	}
	var got strings.Builder
	tm := template.Must(template.New("t").Option("missingkey=error").Parse("{{.PR.URL}} {{.PR.Slug}} {{.PR.Prior.Head}}"))
	if err := tm.Execute(&got, c); err != nil || got.String() != ref7.URL()+" acme/api#7 9f8" {
		t.Fatalf("%q %v", got.String(), err)
	}
}

func TestBuildOnAThreadUnit(t *testing.T) {
	reads := &threadReads{}
	c, err := Builder{Intents: threadIntents(t), Payloads: map[unit.ProviderID]PayloadReader{"thread": reads}, Guides: guides{}}.
		Build(context.Background(), Request{Intent: "discuss", Unit: thread1})
	if err != nil {
		t.Fatal(err)
	}
	if reads.calls != 1 || c.PR() != nil || c.Unit != thread1 || c.Wording.Noun != "Slack thread" || c.Headline != "Discuss #eng thread 1700000000.1, asked for by you." {
		t.Fatalf("calls %d PR %v unit %v wording %+v headline %q", reads.calls, c.PR(), c.Unit, c.Wording, c.Headline)
	}
}

func TestBuildNamesAProviderWithoutReader(t *testing.T) {
	_, err := Builder{Intents: threadIntents(t), Guides: guides{}}.Build(context.Background(), Request{Intent: "discuss", Unit: thread1})
	if err == nil || !strings.Contains(err.Error(), "thread provider") {
		t.Fatalf("err = %v", err)
	}
	_, err = Builder{Intents: bundledIntents, Guides: guides{}}.Build(context.Background(), Request{Intent: Intent(dintent.Review), Unit: ref7.Unit()})
	if err == nil || !strings.Contains(err.Error(), "github provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestIntentOfAnotherProviderIsRefused(t *testing.T) {
	reads := &threadReads{}
	b := Builder{Intents: threadIntents(t), Payloads: map[unit.ProviderID]PayloadReader{"thread": reads, unit.GitHub: GitHub{PRs: &fakePRs{}}}, Guides: guides{}}
	for _, r := range []Request{{Intent: Intent(dintent.Review), Unit: thread1}, {Intent: "discuss", Unit: ref7.Unit()}} {
		_, err := b.Build(context.Background(), r)
		if err == nil || !strings.Contains(err.Error(), "github") || !strings.Contains(err.Error(), "thread") {
			t.Errorf("%s on %v: %v", r.Intent, r.Unit, err)
		}
	}
	if reads.calls != 0 {
		t.Errorf("read %d payloads for a refused turn", reads.calls)
	}
}

func TestSharedTemplatesUseTheProviderWording(t *testing.T) {
	intents := threadIntents(t)
	b := Builder{Intents: intents, Payloads: map[unit.ProviderID]PayloadReader{"thread": &threadReads{}}, Guides: guides{}, Command: "loop"}
	resume := scenario(t, "review/resume/answered-after-push").Context.Resume
	for _, r := range []Request{
		{Intent: "discuss", Unit: thread1},
		{Intent: "discuss", Unit: thread1, Resume: resume, History: []instructions.PastTurn{{Intent: "discuss", Trigger: "by-hand", State: "done"}}},
	} {
		c, err := b.Build(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Render(c, intents(), "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.Text, "pull request") || !strings.Contains(out.Text, "this Slack thread") || !strings.Contains(out.Text, "thread:1700000000.1") {
			t.Errorf("rendered:\n%s", out.Text)
		}
		if r.Resume != nil && !strings.Contains(out.Text, "The Slack thread is now at") {
			t.Errorf("the resume section lacks the noun:\n%s", out.Text)
		}
		if !strings.Contains(out.Text, "none is stored for this Slack thread yet") || !strings.Contains(out.Text, " in #eng thread 1700000000.1.") {
			t.Errorf("the brief or the first line lacks the wording:\n%s", out.Text)
		}
	}
}

func TestPromptLineNamesTheUnit(t *testing.T) {
	gh := scenario(t, "review/first/automated").Context
	if p := Prompt(gh, "abc"); !strings.HasSuffix(p, " --turn abc https://github.com/acme/api/pull/7` and follow what it prints.") {
		t.Errorf("github prompt %q", p)
	}
	c, err := Builder{Intents: threadIntents(t), Payloads: map[unit.ProviderID]PayloadReader{"thread": &threadReads{}}, Guides: guides{}, Command: "loop"}.
		Build(context.Background(), Request{Intent: "discuss", Unit: thread1})
	if err != nil {
		t.Fatal(err)
	}
	if p := Prompt(c, "abc"); !strings.HasSuffix(p, " --turn abc thread:1700000000.1` and follow what it prints.") {
		t.Errorf("thread prompt %q", p)
	}
}

func TestTurnFactsOfASample(t *testing.T) {
	d, _ := bundled(t).Registry.Get(dintent.Review)
	c := samples(d)[0]
	want := map[string]any{
		"pr.mine": false, "pr.author": "sample", "pr.state": "OPEN", "pr.slug": "acme/api#7", "pr.owner": "acme", "pr.repo": "api",
		"pr.base": "main", "pr.association": "MEMBER", "pr.cross_repository": false, "pr.visibility": "", "pr.labels": []string{"sample"},
		"turn.trigger": "by-hand", "turn.automated": false, "turn.owned": false, "turn.needs_take_over": false, "turn.resume": false,
		"turn.plain": false, "turn.planned": "", "turn.round": "first",
		"last_review.exists": true, "last_review.verdict": "", "last_review.decision": "APPROVED",
		"config.address_reviews": false, "config.address_checks": false, "config.followups_without_workspace": false, "config.address_without_workspace": false,
	}
	var seen []string
	for _, f := range builtin.Facts {
		if !slices.Contains(f.Slots, builtin.TurnSlot) {
			continue
		}
		seen = append(seen, f.Name)
		w, ok := want[f.Name]
		if !ok {
			t.Errorf("%s: no expected value", f.Name)
			continue
		}
		if got := (turnFacts{&c}).Get(f.Name); !reflect.DeepEqual(got, w) {
			t.Errorf("%s = %#v, want %#v", f.Name, got, w)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("%d turn facts, %d expected", len(seen), len(want))
	}
	c.PR().Labels = nil
	if got := (turnFacts{&c}).Get("pr.labels"); !reflect.DeepEqual(got, []string{}) {
		t.Errorf("nil labels read %#v", got)
	}
}

func TestThreadTurnReadsNoPRFact(t *testing.T) {
	c := Context{Payload: threadPayload{"1"}}
	for _, name := range []string{"pr.mine", "pr.author", "last_review.decision"} {
		if got := (turnFacts{&c}).Get(name); got != nil && got != "" {
			t.Errorf("%s = %#v on a thread", name, got)
		}
	}
	if got := (turnFacts{&c}).Get("thread.key"); got != "1" {
		t.Errorf("thread.key = %#v", got)
	}
}

type fakeItems struct {
	items []instructions.OpenItem
	asked []unit.Unit
}

func (f *fakeItems) OpenItems(_ context.Context, u unit.Unit) ([]instructions.OpenItem, error) {
	f.asked = append(f.asked, u)
	return f.items, nil
}

func TestBuilderFillsItemsThroughTheReader(t *testing.T) {
	items := &fakeItems{items: []instructions.OpenItem{{}, {}}}
	b := Builder{Intents: threadIntents(t), Payloads: map[unit.ProviderID]PayloadReader{"thread": &threadReads{}}, Guides: guides{}, Items: items}
	c, err := b.Build(context.Background(), Request{Intent: "discuss", Unit: thread1})
	if err != nil || len(c.Items) != 2 || !slices.Equal(items.asked, []unit.Unit{thread1}) {
		t.Fatalf("items %d asked %v err %v", len(c.Items), items.asked, err)
	}
	b.Items = nil
	if c, err = b.Build(context.Background(), Request{Intent: "discuss", Unit: thread1}); err != nil || len(c.Items) != 0 {
		t.Fatalf("items %d err %v", len(c.Items), err)
	}
}

func TestAnAnyIntentBuildsOnEveryProvider(t *testing.T) {
	l, problems := userIntent(t, "anywhere", strings.Replace(threadIntent, `provider = "thread"`, `provider = "any"`, 1), threadBody)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	b := Builder{Intents: func() *intentload.Loaded { return l }, Guides: guides{},
		Payloads: map[unit.ProviderID]PayloadReader{"thread": &threadReads{}, unit.GitHub: GitHub{PRs: &fakePRs{}}}}
	for _, u := range []unit.Unit{thread1, ref7.Unit()} {
		c, err := b.Build(context.Background(), Request{Intent: "anywhere", Unit: u})
		if err != nil || c.Unit != u {
			t.Errorf("%v: unit %v err %v", u, c.Unit, err)
		}
	}
}
