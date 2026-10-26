package turn

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/instructions"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
)

type fakePRs struct {
	pr    PR
	calls int
}

func (f *fakePRs) PR(context.Context, github.Ref) (PR, error) {
	f.calls++
	return f.pr, nil
}

type guides struct {
	err  error
	seen *profile.Facts
}

func (g guides) Guide(kind profile.Kind, f profile.Facts) (profile.Guide, error) {
	if g.seen != nil {
		*g.seen = f
	}
	return profile.Guide{Kind: kind}, g.err
}

type facts struct {
	st    Stored
	err   error
	calls int
}

func (f *facts) Facts(context.Context, unit.Unit) (Stored, error) {
	f.calls++
	return f.st, f.err
}

var ref7 = github.Ref{Owner: "acme", Repo: "api", Number: 7}

func TestBuildUsesTheCallersReadBeforeGitHub(t *testing.T) {
	f := &fakePRs{}
	c, err := Builder{Intents: bundledIntents, Payloads: githubReads(f), Guides: guides{}, Command: brand.Name}.Build(context.Background(),
		Request{Intent: Intent(dintent.Review), Unit: ref7.Unit(), Payload: &PR{Title: "Add retries", Author: "ana", Head: "abc"}})
	if err != nil || f.calls != 0 || c.PR().Title != "Add retries" || c.PR().URL != ref7.URL() || c.PR().Slug() != "acme/api#7" || c.Command != brand.Name {
		t.Fatalf("%+v %v calls=%d", c, err, f.calls)
	}
}

func TestProjectSettingsFollowTheConfigsPredicate(t *testing.T) {
	cfg := config.Config{ProjectSettings: map[string]string{"review": `pr.visibility == "private"`}}
	for visibility, without := range map[string]bool{"private": false, "public": true, "": true} {
		b := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}, Config: cfg}
		c, err := b.Build(context.Background(), Request{Intent: Intent(dintent.Review), Unit: ref7.Unit(), Payload: &PR{Title: "t", Author: "ana", Head: "abc", Visibility: visibility}})
		if err != nil {
			t.Fatal(err)
		}
		if c.WithoutProjectSettings != without {
			t.Errorf("%q: without project settings %v, want %v", visibility, c.WithoutProjectSettings, without)
		}
		r, err := render(c)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(r.Text, "runs without the checkout's own agent settings"); got != without {
			t.Errorf("%q: the instructions say the settings are off: %v", visibility, got)
		}
	}
	c, err := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}, Config: cfg}.Build(context.Background(),
		Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Payload: &PR{Title: "t", Author: "ana", Head: "abc", Visibility: "public"}})
	if err != nil || c.WithoutProjectSettings {
		t.Errorf("an intent the config does not name: without %v, err %v", c.WithoutProjectSettings, err)
	}
}

func TestBuildReadsGitHubOnceWithoutTheCallersRead(t *testing.T) {
	f := &fakePRs{pr: PR{Title: "fetched", Prior: &PriorReview{Verdict: "APPROVED", Head: "9f8"}}}
	c, _ := Builder{Intents: bundledIntents, Payloads: githubReads(f), Guides: guides{}}.Build(context.Background(), Request{Intent: Intent(dintent.Review), Unit: ref7.Unit()})
	if f.calls != 1 || c.PR().Title != "fetched" || c.Round != "follow-up" || c.PR().Number != 7 {
		t.Fatalf("%+v calls=%d", c, f.calls)
	}
}

func TestBuildStopsOnTheGuide(t *testing.T) {
	if _, err := (Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{err: errors.New("profile missing")}}).Build(context.Background(), Request{Intent: Intent(dintent.Review), Unit: ref7.Unit()}); err == nil {
		t.Fatal("built without a guide")
	}
}

func TestBuildComposesWithTheStoredFacts(t *testing.T) {
	var seen profile.Facts
	br := &Brief{Goal: "g", Kind: "fix", Size: "s", Risk: "low", Tags: []string{"lang:go"}, Files: []string{"a.go"}}
	st := &facts{st: Stored{Brief: br, Teams: []string{"acme-platform"}}}
	c, err := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{seen: &seen}, Facts: st}.Build(context.Background(), Request{Intent: Intent(dintent.Review), Unit: ref7.Unit(),
		Payload: &PR{Author: "ana", Association: "FIRST_TIME_CONTRIBUTOR", Base: "main", Labels: []string{"hotfix"}, Prior: &PriorReview{Head: "x"}}})
	if err != nil || c.Brief != br || c.BriefMissing != "" || st.calls != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	want := map[string]any{"pr.owner": "acme", "pr.repo_name": "api", "pr.author": "ana", "pr.association": "FIRST_TIME_CONTRIBUTOR", "pr.base": "main",
		"turn.stage": "follow-up", "brief.kind": "fix", "brief.size": "s", "brief.risk": "low", "pr.requested_teams": []string{"acme-platform"},
		"pr.labels": []string{"hotfix"}, "pr.address.reviewers": []string{}}
	if !reflect.DeepEqual(seen.Values, want) || !seen.HasBrief || strings.Join(seen.Tags, ",") != "lang:go" || strings.Join(seen.Files, ",") != "a.go" {
		t.Fatalf("%+v", seen)
	}
	if _, err := (Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{seen: &seen}, Facts: st}).Build(context.Background(), Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Payload: &PR{}}); err != nil || seen.Values["turn.stage"] != "" {
		t.Fatalf("an address round has a round fact: %q %v", seen.Values["turn.stage"], err)
	}
}

func TestBuildWithoutABrief(t *testing.T) {
	var seen profile.Facts
	for _, c := range []struct {
		b      Builder
		reason string
		want   string
	}{
		{Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{seen: &seen}}, "", noBrief},
		{Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{seen: &seen}, Facts: &facts{}}, "the brief gave up", "the brief gave up"},
		{Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{seen: &seen}, Facts: &facts{err: errors.New("disk")}}, "", "reading it failed (disk)"},
	} {
		got, err := c.b.Build(context.Background(), Request{Intent: Intent(dintent.Review), Unit: ref7.Unit(), Payload: &PR{}, BriefMissing: c.reason})
		if err != nil || got.Brief != nil || got.BriefMissing != c.want || seen.HasBrief || seen.Values["turn.stage"] != "first" {
			t.Errorf("%+v %v %+v", got.BriefMissing, err, seen)
		}
	}
}

func TestAProbeReadsNoFacts(t *testing.T) {
	st := &facts{}
	if _, err := (Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}, Facts: st}).Build(context.Background(), Request{Intent: Intent(dintent.Probe), Unit: ref7.Unit()}); err != nil || st.calls != 0 {
		t.Fatalf("%v calls=%d", err, st.calls)
	}
}

func TestBuildCopiesTheRequestFields(t *testing.T) {
	c, err := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}}.Build(context.Background(),
		Request{Intent: Intent(dintent.Nudge), Unit: ref7.Unit(), Inputs: map[string]any{"situation": "Waiting on maintainers", "waits_on_maintainers": true}, Guidance: "be brief", Agent: "claude",
			History: []instructions.PastTurn{{Intent: "review"}}, Resume: &instructions.Resume{Answer: "go"}})
	if err != nil || c.Inputs["waits_on_maintainers"] != true || c.Inputs["situation"] != "Waiting on maintainers" || c.Guidance != "be brief" || c.Agent != "claude" ||
		len(c.History) != 1 || c.Resume == nil || c.Resume.Answer != "go" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestOnlyAnAddressNeitherOwnedNorTakenOverNeedsTakeOver(t *testing.T) {
	b := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}}
	for _, c := range []struct {
		r    Request
		want bool
	}{
		{Request{Intent: Intent(dintent.Address), Unit: ref7.Unit()}, true},
		{Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Owned: true}, false},
		{Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Inputs: map[string]any{"take_over": true}}, false},
		{Request{Intent: Intent(dintent.Review), Unit: ref7.Unit()}, false},
		{Request{Intent: Intent(dintent.Nudge), Unit: ref7.Unit()}, false},
	} {
		got, err := b.Build(context.Background(), c.r)
		if err != nil || got.NeedsTakeOver != c.want {
			t.Errorf("%+v: %v %v", c.r, got.NeedsTakeOver, err)
		}
	}
}

func TestBuildOfAProbeReadsNeitherGitHubNorAGuide(t *testing.T) {
	f := &fakePRs{}
	probe := github.Ref{Owner: "loop-e2e", Repo: "probe", Number: 1760000000}
	c, err := Builder{Intents: bundledIntents, Payloads: githubReads(f), Guides: guides{err: errors.New("no guide for a probe")}, Command: brand.Name}.Build(context.Background(), Request{Intent: Intent(dintent.Probe), Unit: probe.Unit()})
	if err != nil || f.calls != 0 || c.PR().Title != ProbeTitle || c.PR().URL != probe.URL() || c.PR().Slug() != "loop-e2e/probe#1760000000" || len(c.Guide.Parts) != 0 {
		t.Fatalf("%+v %v calls=%d", c, err, f.calls)
	}
}

func bundledIntents() *intentload.Loaded {
	l, err := Bundled()
	if err != nil {
		panic(err)
	}
	return l
}

type recordingGuides struct {
	kinds *[]profile.Kind
	facts *[]profile.Facts
}

func (g recordingGuides) Guide(kind profile.Kind, f profile.Facts) (profile.Guide, error) {
	*g.kinds = append(*g.kinds, kind)
	*g.facts = append(*g.facts, f)
	return profile.Guide{Kind: kind}, nil
}

var selfReviewOn = map[string]any{"self_review": true}

func TestTheTakeOverQuestionComposesNoReviewGuide(t *testing.T) {
	var seen []profile.Kind
	var facts []profile.Facts
	b := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: recordingGuides{kinds: &seen, facts: &facts}}
	c, err := b.Build(context.Background(), Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Payload: &PR{}, Inputs: selfReviewOn})
	if err != nil || !c.NeedsTakeOver || !slices.Equal(seen, []profile.Kind{addressKind}) || c.Inputs["self_review"] != true {
		t.Fatalf("%v %v %v", seen, c.Inputs, err)
	}
	if _, ok := c.Guides["review"]; ok {
		t.Fatalf("guides %v", c.Guides)
	}
}

func TestASelfReviewComposesTheReviewGuideForAFirstRound(t *testing.T) {
	var seen []profile.Kind
	var facts []profile.Facts
	b := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: recordingGuides{kinds: &seen, facts: &facts}}
	c, err := b.Build(context.Background(), Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Payload: &PR{}, Owned: true, Inputs: selfReviewOn})
	if err != nil || c.Inputs["self_review"] != true || !slices.Equal(seen, []profile.Kind{addressKind, reviewKind}) {
		t.Fatalf("%v %v %v", seen, c.Inputs, err)
	}
	if facts[1].Values["turn.stage"] != "first" || c.Guides["review"].Kind != reviewKind {
		t.Fatalf("facts %+v guides %v", facts, c.Guides)
	}
}

func TestOnlyASelfReviewAddressReadsTheReviewGuide(t *testing.T) {
	var seen []profile.Kind
	var facts []profile.Facts
	b := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: recordingGuides{kinds: &seen, facts: &facts}}
	c, err := b.Build(context.Background(), Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Payload: &PR{}, Owned: true})
	if err != nil || !slices.Equal(seen, []profile.Kind{addressKind}) || c.Inputs["self_review"] != false {
		t.Fatalf("%v %v %v", seen, c.Inputs, err)
	}
	seen = nil
	if _, err := b.Build(context.Background(), Request{Intent: Intent(dintent.Review), Unit: ref7.Unit(), Payload: &PR{}, Inputs: selfReviewOn}); err == nil || !strings.Contains(err.Error(), "inputs.self_review is not an input of review") || len(seen) != 0 {
		t.Fatalf("a review took self_review: %v %v", seen, err)
	}
}

func TestAnAutomatedAddressTakesNoSelfReview(t *testing.T) {
	b := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}}
	if _, err := b.Build(context.Background(), Request{Intent: Intent(dintent.Address), Trigger: Automated, Unit: ref7.Unit(), Payload: &PR{}, Owned: true, Inputs: selfReviewOn}); err == nil || !strings.Contains(err.Error(), "by hand only") {
		t.Fatalf("an automated address took self_review: %v", err)
	}
}

const (
	addressKind = profile.Kind(dintent.Address)
	reviewKind  = profile.Kind(dintent.Review)
)

func TestTheRecordedVerdictAnchorsOnlyAReviewNoNewerThanIt(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 22, 11, 0, 0, time.UTC)
	build := func(reviewedAt time.Time) PriorReview {
		t.Helper()
		pr := PR{Prior: &PriorReview{Verdict: "APPROVED", Head: "drifted", At: reviewedAt}}
		c, err := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{}}.Build(context.Background(),
			Request{Intent: Intent(dintent.Review), Unit: ref7.Unit(), Payload: &pr, LastVerdict: &RecordedVerdict{Head: "c3", Endorsed: "yes: by hand", At: t0.Add(time.Minute)}})
		if err != nil {
			t.Fatal(err)
		}
		return *c.PR().Prior
	}
	if got := build(t0); got.Head != "c3" || got.Endorsed != "yes: by hand" {
		t.Fatalf("the review the record published: %+v", got)
	}
	if got := build(t0.Add(time.Hour)); got.Head != "drifted" || got.Endorsed != "" {
		t.Fatalf("a review newer than the record took the record's head: %+v", got)
	}
}

func githubReads(prs PRReader) map[unit.ProviderID]PayloadReader {
	return map[unit.ProviderID]PayloadReader{unit.GitHub: GitHub{PRs: prs}}
}

func TestAnAddressRoundComposesWithItsReviewers(t *testing.T) {
	var seen profile.Facts
	p := &PR{Address: &RoundReason{Reviewers: []RoundReviewer{{Login: "review-helper[bot]"}}}}
	_, err := Builder{Intents: bundledIntents, Payloads: githubReads(&fakePRs{}), Guides: guides{seen: &seen}}.Build(context.Background(),
		Request{Intent: Intent(dintent.Address), Unit: ref7.Unit(), Payload: p})
	if err != nil || !reflect.DeepEqual(seen.Values["pr.address.reviewers"], []string{"review-helper[bot]"}) {
		t.Fatalf("%+v %v", seen, err)
	}
}
