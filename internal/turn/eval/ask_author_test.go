package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/instructions"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// askAuthor is the eval scenario of the ask-author fixture, a user intent the
// shipped binary cannot load, against the bundled intents plus that fixture.
func askAuthor(t *testing.T) ([]turn.Scenario, *intentload.Loaded) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "intent", "testdata", "good", "ask-author"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	l, problems := intentload.Load([]profile.Layer{{Name: "user", Source: dir, Dir: dir}}, turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	pr := turn.PR{URL: "https://github.com/acme/api/pull/7", Owner: "acme", Repo: "api", Number: 7,
		Title: "Add retries to the webhook sender", Author: "ana", Association: "MEMBER", Head: "1a2b3c4d5e6f", Base: "main"}
	ctx := func(edit func(*turn.Context)) turn.Context {
		pr := pr
		c := turn.Context{Intent: "ask-author", Trigger: turn.ByHand, Payload: &pr, Agent: "claude", Command: "'/opt/" + brand.Name + "/bin/" + brand.Name + "'",
			Inputs: map[string]any{"topic": "the retry cap"}}
		if edit != nil {
			edit(&c)
		}
		return c
	}
	return []turn.Scenario{
		{
			Name:    "ask-author/by-hand/topic",
			Context: ctx(nil),
			Expect: "A by-hand turn of a user intent: the agent reads the pull request, writes one question for its author about the retry cap, " +
				"publishes nothing and records asked with that question for the user. Nothing about reviews, triage, verdicts or publishing.",
			Must:    []string{"# Ask the author of acme/api#7 one question.", "About: the retry cap", "Publish nothing", "Record `asked`", "turn_end", "### Earlier turns"},
			MustNot: []string{"REQUEST_CHANGES", "--take-over", "## Guidance", "## Where the last session stopped"},
		},
		{
			Name: "ask-author/resume/answered",
			Context: ctx(func(c *turn.Context) {
				c.Resume = &instructions.Resume{Question: &instructions.Question{Text: "Is the retry cap configurable, and where?"},
					Head: "1a2b3c4d5e6f", CurrentHead: "1a2b3c4d5e6f", Answer: "yes, but ask only where it is set"}
				c.History = []instructions.PastTurn{{When: time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC), Intent: "ask-author", Trigger: "by-hand",
					State: "asked", Head: "1a2b3c4d5e6f", Summary: "send this question to @ana?"}}
			}),
			Expect: "The resume of an ask-author turn after the user answered its question: the agent must apply the user's rewrite to the drafted question, " +
				"ask nothing again, still publish nothing, and record done with the final question as the summary.",
			Must: []string{"## Where the last session stopped", "Is the retry cap configurable, and where?", "yes, but ask only where it is set", "record `done`", "Publish nothing",
				"Steps 1 to 3 are done", "Go to step 4"},
			MustNot: []string{"first turn " + brand.Name + " has a record of"},
		},
	}, l
}

func TestTheAskAuthorScenariosRender(t *testing.T) {
	scenarios, l := askAuthor(t)
	for _, s := range scenarios {
		c, err := turn.Complete(s.Context, l)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		r, err := turn.Render(c, l, "")
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		for _, m := range s.Must {
			if !strings.Contains(r.Text, m) {
				t.Errorf("%s lacks %q", s.Name, m)
			}
		}
		for _, m := range s.MustNot {
			if strings.Contains(r.Text, m) {
				t.Errorf("%s carries %q", s.Name, m)
			}
		}
	}
}

// TestTheAskAuthorScenariosAreJudged runs the configured agent; only with
// LOOP_EVAL=1, so make test never calls one.
func TestTheAskAuthorScenariosAreJudged(t *testing.T) {
	if os.Getenv("LOOP_EVAL") != "1" {
		t.Skip("set LOOP_EVAL=1 to have an agent judge the ask-author scenarios")
	}
	scenarios, l := askAuthor(t)
	agent := claude.Agent{Sh: claude.Shell, AskSh: claude.Exec, UserHome: config.Home()}
	var results []Result
	for _, s := range scenarios {
		var res Result
		for try := 0; try < 2; try++ {
			var err error
			if res, err = Judge(context.Background(), agent, t.TempDir(), s, l); err != nil {
				t.Fatal(err)
			}
			if res.Verdict.Score >= 4 {
				break
			}
		}
		results = append(results, res)
		if res.Verdict.Score < 4 {
			t.Errorf("%s scored %d: %+v", s.Name, res.Verdict.Score, res.Verdict)
		}
	}
	t.Log("\n" + Report(results))
}
