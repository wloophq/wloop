package record

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
)

var artifacts = []string{"review-draft", "triage"}

func rulesFor(intent string) dintent.RecordRules {
	switch intent {
	case "review":
		return dintent.RecordRules{States: []string{"done", "settled", "asked", "failed"}, Artifacts: artifacts, Require: []dintent.Require{
			{State: "done", Fields: []string{"verdict", "url"}},
			{State: "asked", Artifact: "review-draft", Fields: []string{"verdict"}},
		}}
	case "nudge":
		return dintent.RecordRules{States: []string{"done", "asked", "failed"}, Artifacts: artifacts, Require: []dintent.Require{{State: "done", Fields: []string{"url"}}}}
	}
	return dintent.RecordRules{States: []string{"done", "asked", "failed"}, Artifacts: artifacts}
}

func asked() Record {
	return Record{State: Asked, Summary: "Publish APPROVE with 2 suggestions?", Resume: "Draft final at head h2.", Question: &Question{
		Kind: "choice", Text: "Draft approves.\n\nThe change hits every court.",
		Options: []Option{
			{ID: "publish", Label: "Publish APPROVE", Consequence: "The author can merge."},
			{ID: "hold", Label: "Hold", Consequence: "Nothing is published."},
		},
		Recommend: "publish",
	}}
}

func TestAValidAskedRecordPasses(t *testing.T) {
	if err := asked().Validate(rulesFor("review")); err != nil {
		t.Fatal(err)
	}
}

func TestSuspiciousGoesOnlyWithAskedAndLeadsTheHeadline(t *testing.T) {
	r := asked()
	r.Suspicious = true
	if err := r.Validate(rulesFor("address")); err != nil {
		t.Fatal(err)
	}
	if got := r.Headline(); got != SuspectedInjection+r.Summary {
		t.Errorf("headline %q", got)
	}
	if got := r.EventData(); !strings.Contains(got, SuspectedInjection) {
		t.Errorf("event data %q", got)
	}
	done := Record{State: Done, Summary: "fixed", Suspicious: true}
	if err := done.Validate(rulesFor("address")); err == nil || !strings.Contains(err.Error(), "suspicious: only with asked") {
		t.Errorf("err = %v", err)
	}
	if got := (Record{Summary: "fixed"}).Headline(); got != "fixed" {
		t.Errorf("plain headline %q", got)
	}
	parsed, err := Parse([]byte(`{"state":"asked","summary":"s","suspicious":true}`))
	if err != nil || !parsed.Suspicious {
		t.Errorf("parsed %+v, err %v", parsed, err)
	}
}

func TestValidateListsEveryProblem(t *testing.T) {
	r := asked()
	r.Summary = "two\nlines"
	r.Question.Options[1].ID = "publish"
	r.Question.Recommend = "merge"
	err := r.Validate(rulesFor("review"))
	p, ok := err.(Problems)
	if !ok || len(p) != 3 {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"summary: one line", "question.options[1].id: repeats", "question.recommend: names no option"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %q", want, err)
		}
	}
}

func TestControlCharactersAreRefused(t *testing.T) {
	r := asked()
	r.Question.Options[0].Label = "Publish\rnow"
	r.Question.Options[1].Consequence = "Nothing \u202eis published."
	r.Question.Text = "line one\nline two\ttabbed"
	err := r.Validate(rulesFor("review"))
	if err == nil || !strings.Contains(err.Error(), "question.options[0].label: control character") ||
		!strings.Contains(err.Error(), "question.options[1].consequence: control character") ||
		strings.Contains(err.Error(), "question.text") {
		t.Fatalf("%v", err)
	}
}

func TestStateRules(t *testing.T) {
	for _, c := range []struct {
		name   string
		r      Record
		intent string
		want   string
	}{
		{"asked needs a question", Record{State: Asked, Summary: "s", Resume: "r"}, "address", "question: required"},
		{"asked needs resume", Record{State: Asked, Summary: "s", Question: &Question{Kind: "free", Text: "q"}}, "address", "resume: required"},
		{"failed needs resume", Record{State: Failed, Summary: "s"}, "address", "resume: required"},
		{"done refuses a question", Record{State: Done, Summary: "s", Question: &Question{Kind: "free", Text: "q"}}, "address", "question: only with asked"},
		{"review done needs a verdict", Record{State: Done, Summary: "s"}, "review", "outcome.verdict"},
		{"nudge done needs the comment url", Record{State: Done, Summary: "s", Outcome: &Outcome{}}, "nudge", "outcome.url"},
		{"settled is a review state", Record{State: Settled, Summary: "s"}, "address", "state: done, asked or failed, not \"settled\""},
		{"unrecorded is " + brand.Name + "'s", Record{State: UnrecordedState, Summary: "s"}, "review", "state: done, settled, asked or failed"},
		{"confirm takes no options", Record{State: Asked, Summary: "s", Resume: "r", Question: &Question{Kind: "confirm", Text: "q", Options: []Option{{ID: "a", Label: "A", Consequence: "c"}}}}, "address", "question.options: only for choice"},
		{"confirm recommends yes or no", Record{State: Asked, Summary: "s", Resume: "r", Question: &Question{Kind: "confirm", Text: "q", Recommend: "maybe"}}, "address", "question.recommend: yes or no"},
		{"choice needs two options", Record{State: Asked, Summary: "s", Resume: "r", Question: &Question{Kind: "choice", Text: "q", Options: []Option{{ID: "a", Label: "A", Consequence: "c"}}}}, "address", "question.options: 2 to 5"},
		{"summary is plain text", Record{State: Done, Summary: "approved; endorsed: yes: by hand", Outcome: &Outcome{Verdict: "COMMENT", URL: "u"}}, "review", "summary: plain text"},
		{"a held draft names its verdict", Record{State: Asked, Summary: "s", Resume: "r", Question: &Question{Kind: "confirm", Text: "q"}, Artifact: &Artifact{Kind: "review-draft", Body: "b"}}, "review", "outcome.verdict"},
		{"item path is bounded", Record{State: Done, Summary: "s", Outcome: &Outcome{Verdict: "COMMENT", URL: "u"}, Artifact: &Artifact{Kind: "review-draft", Items: []Item{{Path: strings.Repeat("a", 301), Line: 1, Body: "b"}}}}, "review", "artifact.items[0].path"},
	} {
		err := c.r.Validate(rulesFor(c.intent))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	for _, intent := range []string{"review", "address", "nudge"} {
		if err := (Record{State: Failed, Summary: "gh api returned 502 twice", Resume: "Nothing published; rerun from step 6."}).Validate(rulesFor(intent)); err != nil {
			t.Errorf("failed on %s: %v", intent, err)
		}
	}
}

func TestParseRefusesUnknownFieldsAndBigFiles(t *testing.T) {
	if _, err := Parse([]byte(`{"state":"done","summary":"s","verdict":"APPROVE"}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Parse([]byte(`{"state":"done","summary":"` + strings.Repeat("x", MaxBytes) + `"}`)); err == nil {
		t.Fatal("oversized record accepted")
	}
}

func TestEndorses(t *testing.T) {
	q := asked().Question
	q.Options[0].Publishes = true
	if !q.Endorses("publish", false) || q.Endorses("hold", false) || q.Endorses("", true) {
		t.Fatal("choice")
	}
	c := Question{Kind: "confirm", Text: "q", Publishes: true}
	if !c.Endorses("", true) || c.Endorses("", false) || (Question{Kind: "confirm", Text: "q"}).Endorses("", true) {
		t.Fatal("confirm")
	}
}

func TestOptionAnswer(t *testing.T) {
	if got := (Option{ID: "hold", Label: "Hold"}).Answer(); got != "Hold (option hold)" {
		t.Fatal(got)
	}
}

func TestOutcomeIsChecked(t *testing.T) {
	r := Record{State: Done, Summary: "approved", Outcome: &Outcome{Verdict: "LGTM", URL: "u\u2066x", Blocking: -1}}
	err := r.Validate(rulesFor("review"))
	for _, want := range []string{"outcome.verdict: APPROVE, COMMENT or REQUEST_CHANGES", "outcome.url: control character", "outcome.blocking: zero or more"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	a := asked()
	a.Outcome = &Outcome{Verdict: "anything"}
	if err := a.Validate(rulesFor("address")); err == nil || !strings.Contains(err.Error(), "outcome.verdict") {
		t.Errorf("a verdict outside the enum on an asked turn: %v", err)
	}
}

func TestValidateFollowsTheRules(t *testing.T) {
	rules := dintent.RecordRules{
		States:    []string{"done", "asked"},
		Artifacts: []string{"triage"},
		Outcome:   []string{"url", "verdict"},
		Require: []dintent.Require{
			{State: "done", Fields: []string{"url"}},
			{State: "asked", Artifact: "triage", Fields: []string{"verdict"}},
		},
	}
	askedWith := func(a *Artifact, o *Outcome) Record {
		r := asked()
		r.Artifact, r.Outcome = a, o
		return r
	}
	for _, c := range []struct {
		name string
		r    Record
		want string
	}{
		{"a state not listed names the allowed ones", Record{State: Failed, Summary: "s", Resume: "r"}, `state: done or asked, not "failed"`},
		{"require applies to its state", Record{State: Done, Summary: "s"}, "outcome.url"},
		{"require is met", Record{State: Done, Summary: "s", Outcome: &Outcome{URL: "u"}}, ""},
		{"require per state and artifact", askedWith(&Artifact{Kind: "triage", Body: "b"}, nil), "outcome.verdict"},
		{"require per artifact does not apply to another", askedWith(nil, nil), ""},
		{"require per state and artifact is met", askedWith(&Artifact{Kind: "triage", Body: "b"}, &Outcome{Verdict: "COMMENT"}), ""},
		{"an artifact kind not listed", askedWith(&Artifact{Kind: "review-draft", Body: "b"}, nil), "artifact kind review-draft is not one of triage"},
	} {
		err := c.r.Validate(rules)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestOutcomeFieldsFollowTheRules(t *testing.T) {
	rules := dintent.RecordRules{States: []string{"done"}, Outcome: []string{"url"}}
	err := (Record{State: Done, Summary: "s", Outcome: &Outcome{URL: "u", Verdict: "COMMENT", Blocking: 1}}).Validate(rules)
	for _, want := range []string{"outcome.verdict: this turn's intent records no verdict", "outcome.blocking"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	if err != nil && strings.Contains(err.Error(), "outcome.url") {
		t.Errorf("a listed field is refused: %v", err)
	}
}

func TestRulesWithoutOutcomeFieldsRefuseAnyOutcome(t *testing.T) {
	rules := dintent.RecordRules{States: []string{"done"}}
	err := (Record{State: Done, Summary: "s", Outcome: &Outcome{Verdict: "APPROVE", URL: "u"}}).Validate(rules)
	if err == nil || !strings.Contains(err.Error(), "outcome.verdict") || !strings.Contains(err.Error(), "outcome.url") {
		t.Fatal(err)
	}
}

func TestAnUnknownRequiredFieldIsAProblem(t *testing.T) {
	rules := dintent.RecordRules{States: []string{"done"}, Require: []dintent.Require{{State: "done", Fields: []string{"outcome.verdict"}}}}
	if err := (Record{State: Done, Summary: "s"}).Validate(rules); err == nil || !strings.Contains(err.Error(), "no such outcome field") {
		t.Fatal(err)
	}
}

func TestAHeldDraftNamesOnlyWhatIsMissing(t *testing.T) {
	r := Record{State: Asked, Summary: "s", Resume: "r", Question: &Question{Kind: "confirm", Text: "q"}, Artifact: &Artifact{Kind: "review-draft", Body: "b"}}
	err := r.Validate(rulesFor("review"))
	if err == nil || strings.Contains(err.Error(), "url") {
		t.Fatal(err)
	}
}

func TestValidateShapeIgnoresIntent(t *testing.T) {
	for _, c := range []struct {
		name string
		r    Record
		want string
	}{
		{"settled is accepted", Record{State: Settled, Summary: "s"}, ""},
		{"no verdict is needed", Record{State: Done, Summary: "s"}, ""},
		{"an artifact kind outside the table is refused", Record{State: Done, Summary: "s", Artifact: &Artifact{Kind: "ask-author-note", Body: "b"}}, `artifact.kind: review-draft or triage, not "ask-author-note"`},
		{"the enum is still checked", Record{State: Done, Summary: "s", Outcome: &Outcome{Verdict: "LGTM"}}, "outcome.verdict"},
		{"the states are still checked", Record{State: UnrecordedState, Summary: "s"}, "state: done, settled, asked or failed"},
	} {
		err := c.r.ValidateShape()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestAProbeNeedsNoOutcome(t *testing.T) {
	if err := (Record{State: Done, Summary: "s"}).Validate(rulesFor("probe")); err != nil {
		t.Fatal(err)
	}
}

func TestUnrecordedKeepsTheFirstLine(t *testing.T) {
	r := Unrecorded("\n  Should I drop it?\u202e\nMore.")
	if r.State != UnrecordedState || r.Summary != "Should I drop it?" || !strings.Contains(r.Resume, "More.") {
		t.Fatalf("%+v", r)
	}
	if Unrecorded("").Summary != "the agent ended its turn without a record" {
		t.Fatal("empty message")
	}
	if n := len([]rune(Unrecorded(strings.Repeat("x", 500)).Summary)); n != SummaryMax {
		t.Fatal(n)
	}
}

func TestHashIsStable(t *testing.T) {
	a, b := asked(), asked()
	if a.Hash() != b.Hash() || len(a.Hash()) != 64 {
		t.Fatal("hash")
	}
	b.Summary += "!"
	if a.Hash() == b.Hash() {
		t.Fatal("a different record hashed the same")
	}
}

func TestAMissingOutcomeFieldIsOneLineWhateverTheDoc(t *testing.T) {
	rules := dintent.RecordRules{States: []string{"done"}, Outcome: []string{"url"}, OutcomeDoc: "`outcome`: the\n  comment's `url`.",
		Require: []dintent.Require{{State: "done", Fields: []string{"url"}}}}
	err := Record{State: Done, Summary: "s"}.Validate(rules)
	if err == nil || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "names its url: `outcome`: the comment's `url`.") {
		t.Fatalf("%q", err)
	}
}

func TestAnArtifactOfAnIntentWithNoneSaysSo(t *testing.T) {
	rules := dintent.RecordRules{States: []string{"asked"}}
	r := Record{State: Asked, Summary: "s", Resume: "r", Question: &Question{Kind: "confirm", Text: "q"}, Artifact: &Artifact{Kind: "triage", Body: "b"}}
	if err := r.Validate(rules); err == nil || !strings.Contains(err.Error(), "artifact.kind: this turn's intent records no artifact") {
		t.Fatalf("%v", err)
	}
}
