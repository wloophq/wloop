package turn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dpr "github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func TestBundledManifestsLoad(t *testing.T) {
	l := bundled(t)
	var names []string
	for _, d := range l.Registry.All() {
		names = append(names, string(d.Name))
		if !d.Bundled {
			t.Errorf("%s is not marked bundled", d.Name)
		}
		p := l.Programs[d.Name]
		if p.Records == nil || len(p.Headline) != len(d.Headline.New) || (d.Action != nil && (p.Visible == nil || p.Enabled == nil || p.Primary == nil)) {
			t.Errorf("%s: a predicate is not compiled: %+v", d.Name, p)
		}
		for i, c := range d.Headline.New {
			if (c.When.Src != "") != (p.Headline[i] != nil) {
				t.Errorf("%s: headline case %d compiled %v", d.Name, i, p.Headline[i] != nil)
			}
		}
		for _, q := range d.Record.Require {
			for _, f := range q.Fields {
				if !slices.Contains(record.OutcomeFields, f) {
					t.Errorf("%s: record.require names %s, not an outcome field", d.Name, f)
				}
			}
		}
	}
	if strings.Join(names, ",") != "act,address,brief,nudge,probe,review" || len(l.Problems) != 0 {
		t.Fatalf("%v %v", names, l.Problems)
	}
	if p, _ := l.Registry.Get(dintent.Probe); p.UserFacing {
		t.Fatal("probe is user-facing")
	}
}

func TestEveryArtifactKindHasARecordSentence(t *testing.T) {
	b, err := Base()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range record.ArtifactKinds {
		var out strings.Builder
		if err := b.ExecuteTemplate(&out, "artifact", k); err != nil || !strings.HasPrefix(out.String(), "`artifact` (asked") || !strings.Contains(out.String(), "`kind: "+k+"`") {
			t.Errorf("%s: %q %v", k, out.String(), err)
		}
	}
}

func TestAnAddressTurnCannotRecordAReviewDraft(t *testing.T) {
	rules, err := bundled(t).Registry.Rules("address")
	if err != nil {
		t.Fatal(err)
	}
	r := record.Record{State: record.Asked, Summary: "s", Resume: "r", Question: &record.Question{Kind: "confirm", Text: "q"},
		Artifact: &record.Artifact{Kind: "review-draft", Body: "b"}}
	if err := r.Validate(rules); err == nil || !strings.Contains(err.Error(), "artifact.kind") {
		t.Fatalf("an address turn recorded a review draft: %v", err)
	}
	r.Artifact.Kind = "triage"
	if err := r.Validate(rules); err != nil {
		t.Fatal(err)
	}
}

func TestANudgeCannotRecordAQuestion(t *testing.T) {
	rules, _ := bundled(t).Registry.Rules("nudge")
	r := record.Record{State: record.Asked, Summary: "s", Resume: "r", Question: &record.Question{Kind: "confirm", Text: "q"}}
	if err := r.Validate(rules); err == nil {
		t.Fatal("a nudge recorded asked, which nobody can answer")
	}
}

func TestCompleteNormalizesInputs(t *testing.T) {
	l := bundled(t)
	c, err := Complete(Context{Intent: Intent(dintent.Nudge), Payload: pr(), Inputs: map[string]any{"who": []any{"@bob", "@ana"}, "waits_on_maintainers": true, "situation": "s"}}, l)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"who": []string{"@bob", "@ana"}, "waits_on_maintainers": true, "situation": "s"}
	if !reflect.DeepEqual(c.Inputs, want) {
		t.Fatalf("%#v", c.Inputs)
	}
	for _, bad := range []map[string]any{
		{"who": []any{"@bob", 3.0}},
		{"who": "@bob"},
		{"waits_on_maintainers": "yes"},
		{"situation": 1.0},
		{"undeclared": true},
	} {
		if _, err := Complete(Context{Intent: Intent(dintent.Nudge), Payload: pr(), Inputs: bad}, l); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestAnUnsetInputRendersAsItsZeroValue(t *testing.T) {
	l := bundled(t)
	plain := completed(t, Context{Intent: Intent(dintent.Address), Payload: pr(), Owned: true})
	if plain.Inputs["take_over"] != false {
		t.Fatalf("%#v", plain.Inputs)
	}
	if _, err := Render(plain, l, ""); err != nil {
		t.Fatal(err)
	}
	resumed := completed(t, Context{Intent: Intent(dintent.Nudge), Payload: pr(), Inputs: map[string]any{"who": []any{"@bob"}}, Resume: scenario(t, "address/resume/continue").Context.Resume})
	if resumed.Inputs["situation"] != "" || resumed.Inputs["waits_on_maintainers"] != false {
		t.Fatalf("%#v", resumed.Inputs)
	}
	if r, err := Render(resumed, l, ""); err != nil || !strings.Contains(r.Text, "# Continue the nudge on acme/api#7") {
		t.Fatalf("%v %s", err, r.Text)
	}
}

func TestRenderRefusesAContextMissingADeclaredInput(t *testing.T) {
	l := bundled(t)
	c := completed(t, Context{Intent: Intent(dintent.Nudge), Payload: pr()})
	delete(c.Inputs, "situation")
	if _, err := Render(c, l, ""); err == nil || !strings.Contains(err.Error(), "inputs.situation") {
		t.Fatalf("rendered without situation: %v", err)
	}
	if _, err := Render(Context{Intent: Intent(dintent.Nudge), Payload: pr()}, l, ""); err == nil {
		t.Fatal("rendered a turn that was not completed")
	}
}

// userIntent writes intents/<name>/ into a user layer and loads it with the bundled intents.
func userIntent(t *testing.T, name, manifest, tmpl string) (*intentload.Loaded, intentload.Problems) {
	t.Helper()
	dir := t.TempDir()
	at := filepath.Join(dir, "intents", name)
	if err := os.MkdirAll(at, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(at, "intent.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(at, "instructions.md.tmpl"), []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := Base()
	if err != nil {
		t.Fatal(err)
	}
	return intentload.Load([]profile.Layer{{Name: "user", Source: dir, Dir: dir}}, BundledFS(), b, CheckTemplate)
}

const askAuthor = `label = "Ask author"
summary = "Ask the author one question"
workspace = "review"
session = { strategy = "new", title = "ask" }
profile = "review"

[inputs.loud]
type = "flag"
flag = "--loud"
help = "asks loudly"
by_hand_only = true

[[also_profiles]]
profile = "nudge"
when    = "inputs.loud"
define  = "nudge-judgment"

[headline]
resumed = "ask round on {{.PR.Slug}}"
[[headline.new]]
text = "Ask the author of {{.PR.Slug}}, {{.Who}}."

[record]
states = ["done", "asked", "failed"]
`

func TestCheckTemplateCatchesExecErrors(t *testing.T) {
	_, problems := userIntent(t, "ask-author", askAuthor, "{{define \"nudge-judgment\"}}{{end}}# {{.Headline}}\n\n{{if .Resume}}{{.Resume.Nope}}{{end}}\n{{if .Inputs.loud}}\n{{.Nope}}{{end}}\n")
	got := problems.Error()
	for _, want := range []string{"intents/ask-author/instructions.md.tmpl:3:", "intents/ask-author/instructions.md.tmpl:5:", "can't evaluate field Nope"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestAByHandOnlyInputIsRefusedOnAnAutomatedTurn(t *testing.T) {
	l, problems := userIntent(t, "ask-author", askAuthor, "{{define \"nudge-judgment\"}}{{end}}# {{.Headline}}\n")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if _, err := Complete(Context{Intent: "ask-author", Trigger: Automated, Payload: pr(), Inputs: map[string]any{"loud": true}}, l); err == nil || !strings.Contains(err.Error(), "by hand only") {
		t.Fatalf("an automated turn set a by-hand-only input: %v", err)
	}
	if _, err := Complete(Context{Intent: "ask-author", Payload: pr(), Inputs: map[string]any{"loud": true}}, l); err != nil {
		t.Fatal(err)
	}
}

func TestAlsoProfilesComposeTheirGuideWhenTheyApply(t *testing.T) {
	l, problems := userIntent(t, "ask-author", askAuthor, "{{define \"nudge-judgment\"}}{{end}}# {{.Headline}}\n")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var kinds []profile.Kind
	g := kindGuides{seen: &kinds}
	b := Builder{Intents: func() *intentload.Loaded { return l }, Payloads: githubReads(&fakePRs{}), Guides: g}
	c, err := b.Build(context.Background(), Request{Intent: "ask-author", Unit: ref7.Unit(), Payload: &PR{}, Inputs: map[string]any{"loud": true}})
	if err != nil || !slices.Equal(kinds, []profile.Kind{"review", "nudge"}) || len(c.Guides) != 2 {
		t.Fatalf("%v %v %v", kinds, c.Guides, err)
	}
	r, err := Render(c, l, "")
	if err != nil || !slices.Contains(r.Sources, "nudge guide: user: /n.md") {
		t.Fatalf("%v %v", r.Sources, err)
	}
	kinds = nil
	if c, err = b.Build(context.Background(), Request{Intent: "ask-author", Unit: ref7.Unit(), Payload: &PR{}}); err != nil || !slices.Equal(kinds, []profile.Kind{"review"}) || len(c.Guides) != 1 {
		t.Fatalf("%v %v", kinds, err)
	}
}

type kindGuides struct{ seen *[]profile.Kind }

func (g kindGuides) Guide(kind profile.Kind, _ profile.Facts) (profile.Guide, error) {
	*g.seen = append(*g.seen, kind)
	return profile.Guide{Kind: kind, Parts: []profile.Part{{Layer: profile.Layer{Name: "user"}, File: "/" + string(kind)[:1] + ".md"}}}, nil
}

const mineReader = `label = "Mine"
summary = "Reads pr.mine"
workspace = "review"
session = { strategy = "new", title = "mine" }
records = "!pr.mine"

[headline]
resumed = "mine on {{.PR.Slug}}"
[[headline.new]]
text = "Mine on {{.PR.Slug}}."

[record]
states = ["done", "failed"]
`

func TestTurnFactsCoverTheTurnSlot(t *testing.T) {
	l, problems := userIntent(t, "mine-reader", mineReader, "# {{.Headline}}\n")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	base := func() (Builder, Request) {
		return Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}, Viewer: func(context.Context) (string, error) { return "me", nil }},
			Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Payload: &PR{Author: "ana"}}
	}
	sourcesOf := func(field string) func() (Builder, Request) {
		if field != "pr.mine" {
			return base
		}
		return func() (Builder, Request) {
			b, r := base()
			b.Intents, r.Intent = func() *intentload.Loaded { return l }, "mine-reader"
			return b, r
		}
	}
	sources := map[string]func(*Builder, *Request){
		"pr.mine":                 func(_ *Builder, r *Request) { r.Payload.(*PR).Author = "Me" },
		"pr.author":               func(_ *Builder, r *Request) { r.Payload.(*PR).Author = "bob" },
		"pr.state":                func(_ *Builder, r *Request) { r.Payload.(*PR).State = "OPEN" },
		"pr.slug":                 func(_ *Builder, r *Request) { r.Unit = dpr.Ref{Owner: "acme", Repo: "api", Number: 8}.Unit() },
		"pr.owner":                func(_ *Builder, r *Request) { r.Unit = dpr.Ref{Owner: "other", Repo: "api", Number: 7}.Unit() },
		"pr.repo_name":            func(_ *Builder, r *Request) { r.Unit = dpr.Ref{Owner: "acme", Repo: "web", Number: 7}.Unit() },
		"pr.base":                 func(_ *Builder, r *Request) { r.Payload.(*PR).Base = "main" },
		"pr.association":          func(_ *Builder, r *Request) { r.Payload.(*PR).Association = "MEMBER" },
		"pr.cross_repository":     func(_ *Builder, r *Request) { r.Payload.(*PR).CrossRepository = true },
		"pr.visibility":           func(_ *Builder, r *Request) { r.Payload.(*PR).Visibility = "private" },
		"pr.labels":               func(_ *Builder, r *Request) { r.Payload.(*PR).Labels = []string{"x"} },
		"pr.requested_teams":      func(b *Builder, _ *Request) { b.Facts = &facts{st: Stored{Teams: []string{"acme-platform"}}} },
		"turn.trigger":            func(_ *Builder, r *Request) { r.Trigger = Automated },
		"turn.automated":          func(_ *Builder, r *Request) { r.Trigger = Automated },
		"unit.ref":                func(_ *Builder, r *Request) { r.Unit = dpr.Ref{Owner: "acme", Repo: "api", Number: 8}.Unit() },
		"turn.owned":              func(_ *Builder, r *Request) { r.Owned = true },
		"turn.needs_take_over":    func(_ *Builder, r *Request) { r.Owned = true },
		"turn.resume":             func(_ *Builder, r *Request) { r.Resume = scenario(t, "address/resume/continue").Context.Resume },
		"turn.try":                func(_ *Builder, r *Request) { r.Plain = true },
		"turn.planned":            func(_ *Builder, r *Request) { r.Planned = PlannedFirst },
		"turn.stage":              func(_ *Builder, r *Request) { r.Intent = Intent(dintent.Review) },
		"pr.last_review.exists":   func(_ *Builder, r *Request) { r.Payload.(*PR).Prior = &PriorReview{} },
		"pr.last_review.verdict":  func(_ *Builder, r *Request) { r.Payload.(*PR).LastReviewVerdict = "COMMENTED" },
		"pr.last_review.decision": func(_ *Builder, r *Request) { r.Payload.(*PR).Prior = &PriorReview{Verdict: "APPROVED"} },
		"pr.address.reviewers": func(_ *Builder, r *Request) {
			r.Payload.(*PR).Address = &RoundReason{Reviewers: []RoundReviewer{{Login: "ana"}}}
		},
		"pr.address.checks": func(_ *Builder, r *Request) {
			r.Payload.(*PR).Address = &RoundReason{Checks: []FailingCheck{{Name: "ci/test"}}}
		},
		"pr.address.conflict": func(_ *Builder, r *Request) {
			r.Payload.(*PR).Address = &RoundReason{Conflict: &RoundConflict{Base: "main"}}
		},
		"config.github.address_reviews": func(b *Builder, _ *Request) { b.Config = config.Config{GitHub: config.GitHub{AddressReviews: true}} },
		"config.github.address_checks":  func(b *Builder, _ *Request) { b.Config = config.Config{GitHub: config.GitHub{AddressChecks: true}} },
		"config.github.followups_without_workspace": func(b *Builder, _ *Request) {
			b.Config = config.Config{GitHub: config.GitHub{FollowupsWithoutWorkspace: true}}
		},
		"config.github.address_without_workspace": func(b *Builder, _ *Request) {
			b.Config = config.Config{GitHub: config.GitHub{AddressWithoutWorkspace: true}}
		},
	}
	get := func(b Builder, r Request, field string) any {
		c, err := b.Build(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		return turnFacts{&c}.Get(field)
	}
	for _, f := range builtin.Facts {
		if !slices.Contains(f.Slots, builtin.TurnSlot) {
			continue
		}
		set, ok := sources[f.Name]
		if !ok {
			t.Errorf("%s has no source in the turn slot", f.Name)
			continue
		}
		start := sourcesOf(f.Name)
		b, r := start()
		before := get(b, r, f.Name)
		b, r = start()
		set(&b, &r)
		if after := get(b, r, f.Name); reflect.DeepEqual(before, after) || before == nil {
			t.Errorf("%s does not follow its source: %v, then %v", f.Name, before, after)
		}
	}
}

func TestATurnThatNeverReadsPrMineReadsNoViewer(t *testing.T) {
	asked := 0
	viewer := func(context.Context) (string, error) { asked++; return "", errors.New("rate limited") }
	if _, err := (Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}, Viewer: viewer}).Build(context.Background(), Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Payload: &PR{}}); err != nil || asked != 0 {
		t.Fatalf("%v, asked %d", err, asked)
	}
	l, problems := userIntent(t, "mine-reader", mineReader, "# {{.Headline}}\n")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	b := Builder{Intents: func() *intentload.Loaded { return l }, Payloads: githubReads(&fakePRs{}), Guides: guides{}, Viewer: viewer}
	if _, err := b.Build(context.Background(), Request{Intent: "mine-reader", Unit: ref7.Unit(), Payload: &PR{}}); err == nil || asked != 1 {
		t.Fatalf("a turn reading pr.mine went on without the viewer: %v, asked %d", err, asked)
	}
}
