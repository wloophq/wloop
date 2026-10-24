package brief

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/usage"
	"github.com/wloophq/wloop/internal/github"
)

const goodTags = `{"lang":["Go"],"framework":[],"tool":[],"area":["jobs"],"change":["feature"],"concern":[],"breaking":[],"domain":["billing"],"audience":[]}`

const goodBrief = `{"goal":"Export invoices in batches","context":"Billing area","kind":"feature","size":"m","risk":"medium","risk_why":"touches the export job","open_questions":["What is the batch size?"],"tags":` + goodTags + `}`

type fakeAsker struct {
	got turns.Ask
	ans turns.Answer
	err error
}

func (f *fakeAsker) Ask(_ context.Context, a turns.Ask) (turns.Answer, error) {
	f.got = a
	return f.ans, f.err
}

func TestAgentAsksForTheTierWithTheSchemaAndABudget(t *testing.T) {
	tokens := &usage.Tokens{Input: 2, Output: 300}
	f := &fakeAsker{ans: turns.Answer{JSON: []byte(goodBrief), Model: "m1", CostUSD: ptr(0.011), Tokens: tokens}}
	r, err := AskRunner{Asker: f}.Run(context.Background(), Call{Tier: turns.TierFast, Home: "/p", Prompt: "the prompt"})
	if err != nil || r.Brief.Kind != "feature" || r.Model != "m1" || *r.CostUSD != 0.011 || r.Tokens != tokens || len(r.Brief.OpenQuestions) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if want := (turns.Ask{Home: "/p", Tier: turns.TierFast, Prompt: "the prompt", Schema: Schema, MaxCostUSD: maxCostUSD}); !reflect.DeepEqual(f.got, want) {
		t.Fatalf("asked %+v, want %+v", f.got, want)
	}
}

func TestAgentKeepsPerPullRequestFailuresWithTheirPullRequest(t *testing.T) {
	for _, c := range []struct {
		err   error
		perPR bool
	}{
		{fmt.Errorf("%w: Exceeded USD budget (0.25)", turns.ErrOverBudget), true},
		{fmt.Errorf("%w: Failed to provide valid structured output", turns.ErrUnfit), true},
		{fmt.Errorf("%w: Prompt is too long", turns.ErrTooLong), true},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), true},
		{fmt.Errorf("x: %w", turns.ErrNoAgent), false},
		{fmt.Errorf("%w: claude: API Error: 401", turns.ErrUnavailable), false},
		{errors.New("claude: API Error: 500"), true},
	} {
		_, err := AskRunner{Asker: &fakeAsker{err: c.err}}.Run(context.Background(), Call{})
		if err == nil || !strings.Contains(err.Error(), c.err.Error()) || global(err) == c.perPR {
			t.Errorf("%v: err %v, global %v", c.err, err, global(err))
		}
	}
}

// global is the queue's class of err on a first failure.
func global(err error) bool {
	g, _ := (&Queue{}).classify(github.Ref{}, err, outcome(err, nil))
	return g
}

func TestAnUnfitAnswerReadsAsOneSentence(t *testing.T) {
	_, err := AskRunner{Asker: &fakeAsker{err: fmt.Errorf("%w: claude: Failed to provide valid structured output", turns.ErrUnfit)}}.Run(context.Background(), Call{})
	if strings.Count(err.Error(), "does not fit the schema") != 1 || global(err) {
		t.Fatalf("err %v", err)
	}
}

func TestDecodeRejectsJSONOutsideTheSchema(t *testing.T) {
	for _, bad := range []string{
		strings.Replace(goodBrief, `"feature"`, `"epic"`, 1),
		strings.Replace(goodBrief, `"medium"`, `"huge"`, 1),
		strings.Replace(goodBrief, `"goal":"Export invoices in batches"`, `"goal":""`, 1),
		strings.Replace(goodBrief, `["What is the batch size?"]`, `["a","b","c","d"]`, 1),
		strings.Replace(goodBrief, `"kind"`, `"extra":1,"kind"`, 1),
		strings.Replace(goodBrief, `,"risk_why":"touches the export job"`, ``, 1),
		strings.Replace(goodBrief, `,"open_questions":["What is the batch size?"]`, ``, 1),
		strings.Replace(goodBrief, goodTags, `["lang:go"]`, 1),
		goodBrief + ` {"goal":"more"}`,
		`not json`,
	} {
		if _, err := (AskRunner{Asker: &fakeAsker{ans: turns.Answer{JSON: []byte(bad)}}}).Run(context.Background(), Call{}); !errors.Is(err, ErrInvalid) || global(err) {
			t.Errorf("%s: err %v", bad, err)
		}
	}
}

func TestAgentNormalizesTheTags(t *testing.T) {
	r, err := AskRunner{Asker: &fakeAsker{ans: turns.Answer{JSON: []byte(goodBrief)}}}.Run(context.Background(), Call{})
	if err != nil || !reflect.DeepEqual(r.Brief.Tags, []string{"lang:go", "area:jobs", "change:feature", "domain:billing"}) || r.DroppedTags != nil {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestABadTagDoesNotFailTheBrief(t *testing.T) {
	bad := strings.Replace(goodBrief, `"lang":["Go"]`, `"lang":["Go","3d","C++"]`, 1)
	bad = strings.Replace(bad, `"change":["feature"]`, `"change":["banana"]`, 1)
	r, err := AskRunner{Asker: &fakeAsker{ans: turns.Answer{JSON: []byte(bad)}}}.Run(context.Background(), Call{})
	if err != nil || !reflect.DeepEqual(r.Brief.Tags, []string{"lang:go", "lang:cpp", "area:jobs", "domain:billing"}) ||
		!reflect.DeepEqual(r.DroppedTags, []string{"lang:3d", "change:banana"}) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestBriefWithoutTagsIsInvalid(t *testing.T) {
	for _, bad := range []string{
		strings.Replace(goodBrief, `,"tags":`+goodTags, ``, 1),
		strings.Replace(goodBrief, goodTags, `null`, 1),
	} {
		if _, err := (AskRunner{Asker: &fakeAsker{ans: turns.Answer{JSON: []byte(bad)}}}).Run(context.Background(), Call{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err %v", bad, err)
		}
	}
}

func TestAFailedCallKeepsWhatItCost(t *testing.T) {
	tokens := &usage.Tokens{Input: 9, Output: 900}
	for _, f := range []*fakeAsker{
		{ans: turns.Answer{Model: "m1", CostUSD: ptr(0.25), Tokens: tokens}, err: fmt.Errorf("%w: claude: over", turns.ErrOverBudget)},
		{ans: turns.Answer{JSON: []byte(`not json`), Model: "m1", CostUSD: ptr(0.25), Tokens: tokens}},
	} {
		r, err := AskRunner{Asker: f}.Run(context.Background(), Call{})
		if err == nil || r.CostUSD == nil || *r.CostUSD != 0.25 || r.Model != "m1" || r.Tokens != tokens {
			t.Errorf("%+v %v", r, err)
		}
	}
}
