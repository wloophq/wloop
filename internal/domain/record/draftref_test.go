package record

import (
	"errors"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"slices"
	"strings"
	"testing"
)

func TestPublishesStillDecodes(t *testing.T) {
	old := `{"state":"asked","summary":"s","resume":"r","question":{"kind":"choice","text":"q","options":[` +
		`{"id":"publish","label":"Publish","consequence":"c","publishes":true},{"id":"hold","label":"Hold","consequence":"c"}]}}`
	r, err := Parse([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Question.Options[0].Publishes || r.Question.Options[0].Draft != nil {
		t.Fatalf("old record: %+v", r.Question.Options)
	}
	confirm := `{"state":"asked","summary":"s","resume":"r","question":{"kind":"confirm","text":"q","publishes":true}}`
	if r, err = Parse([]byte(confirm)); err != nil || !r.Question.Publishes {
		t.Fatalf("old confirm: %+v %v", r.Question, err)
	}
	pinned := `{"state":"asked","summary":"s","resume":"r","question":{"kind":"choice","text":"q","options":[` +
		`{"id":"approve","label":"Approve","consequence":"c","draft":{"id":42,"rev":1}},{"id":"comment","label":"Comment","consequence":"c","draft":{"id":43,"rev":2}}]}}`
	if r, err = Parse([]byte(pinned)); err != nil {
		t.Fatal(err)
	}
	if d := r.Question.Options[1].Draft; d == nil || *d != (DraftRef{ID: 43, Rev: 2}) {
		t.Fatalf("%+v", r.Question.Options)
	}
	if err := r.ValidateShape(); err != nil {
		t.Fatal(err)
	}
}

func TestOptionDraftsAreChecked(t *testing.T) {
	r := asked()
	r.Question.Options[0].Draft = &DraftRef{ID: 42, Rev: 0}
	r.Question.Options[1].Draft = &DraftRef{ID: 42, Rev: 1}
	problems := problemsOf(t, r.ValidateShape())
	for _, want := range []string{"question.options[0].draft", "question.options[1].draft"} {
		if !hasProblem(problems, want) {
			t.Fatalf("%s not refused: %v", want, problems)
		}
	}
	free := Record{State: Asked, Summary: "s", Resume: "r", Question: &Question{Kind: "free", Text: "q", Draft: &DraftRef{ID: 1, Rev: 1}}}
	if p := problemsOf(t, free.ValidateShape()); !hasProblem(p, "question.draft") {
		t.Fatalf("a free question's draft: %v", p)
	}
}

func TestOutcomeDraftStandsForTheURL(t *testing.T) {
	review := rulesFor("review")
	review.Outcome = []string{"verdict", "url", "draft"}
	done := Record{State: Done, Summary: "drafted", Outcome: &Outcome{Verdict: "REQUEST_CHANGES", Draft: &DraftRef{ID: 7, Rev: 1}}}
	if err := done.Validate(review); err != nil {
		t.Fatalf("done with a draft instead of a url: %v", err)
	}
	a := asked()
	a.Outcome = &Outcome{Verdict: "APPROVE", Draft: &DraftRef{ID: 7, Rev: 1}}
	if p := problemsOf(t, a.ValidateShape()); !hasProblem(p, "outcome.draft") {
		t.Fatalf("an asked record's outcome draft: %v", p)
	}
	bad := Record{State: Done, Summary: "drafted", Outcome: &Outcome{Verdict: "COMMENT", Draft: &DraftRef{ID: 7}}}
	if p := problemsOf(t, bad.Validate(review)); !hasProblem(p, "outcome.draft") {
		t.Fatalf("a draft with no rev: %v", p)
	}
	noURL := Record{State: Done, Summary: "drafted", Outcome: &Outcome{Draft: &DraftRef{ID: 7, Rev: 1}}}
	if p := problemsOf(t, noURL.Validate(rulesFor(""))); !hasProblem(p, "outcome.draft") {
		t.Fatalf("a draft where the intent records none: %v", p)
	}
}

func TestAnIntentMayRequireTheDraft(t *testing.T) {
	rules := rulesFor("")
	rules.Outcome = []string{"url", "draft"}
	rules.Require = []dintent.Require{{State: "done", Fields: []string{"draft"}}}
	if p := problemsOf(t, Record{State: Done, Summary: "s", Outcome: &Outcome{URL: "u"}}.Validate(rules)); !hasProblem(p, "outcome.draft") {
		t.Fatalf("a url where the draft is required: %v", p)
	}
	if err := (Record{State: Done, Summary: "s", Outcome: &Outcome{Draft: &DraftRef{ID: 1, Rev: 1}}}).Validate(rules); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyTheLoopRecordsAnApproval(t *testing.T) {
	r, err := Parse([]byte(`{"state":"done","summary":"s","outcome":{"verdict":"APPROVE","url":"u","approved":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	review := rulesFor("review")
	review.Outcome = []string{"verdict", "url", "draft"}
	if p := problemsOf(t, r.Validate(review)); !hasProblem(p, "outcome.approved") {
		t.Fatalf("an agent's approved outcome: %v", p)
	}
	if p := problemsOf(t, r.ValidateShape()); !hasProblem(p, "outcome.approved") {
		t.Fatalf("an agent's approved outcome, shape only: %v", p)
	}
}

func problemsOf(t *testing.T, err error) Problems {
	t.Helper()
	var p Problems
	if !errors.As(err, &p) {
		t.Fatalf("want problems, got %v", err)
	}
	return p
}

func hasProblem(p Problems, field string) bool {
	return slices.ContainsFunc(p, func(s string) bool { return strings.HasPrefix(s, field+":") })
}
