package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func bookFixture() api.TurnBook {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 19, h, m, 0, 0, time.UTC) }
	return api.TurnBook{PR: pr.Ref{Owner: "acme", Repo: "docs", Number: 407}, Turns: []api.TurnShown{
		{
			Turn: dturn.Turn{Seq: 26, Intent: "review", ID: "7bd21eee5264", Trigger: dturn.ByHand, State: dturn.Done,
				Guidance: "Do we really want to delete these documents? Is the procedure safe and the most efficient one for this kind of problem?",
				Session:  "term_6ed72265", Agent: "claude", AgentSession: "c2d6396d",
				SentAt: at(14, 23), StartedAt: at(14, 23), EndedAt: at(14, 32)},
			Instructions: "Instructions 7bd21eee5264 for review in acme/docs.",
			Records: []dturn.Record{
				{ID: 36, TurnSeq: 26, At: at(14, 24), Record: record.Record{State: "unrecorded", Summary: "The review goes on: four subagents run, and the record comes after their findings are merged."}},
				{ID: 37, TurnSeq: 26, At: at(14, 29), Record: record.Record{State: "asked", Summary: "The spec lags behind what shipped; request changes against 3 human approvals?",
					Outcome: &record.Outcome{Verdict: "REQUEST_CHANGES", Blocking: 5, NonBlocking: 11},
					Question: &record.Question{Kind: "choice", Text: "The deletion already ran and the monthly job is live. Publish the REQUEST_CHANGES (5 blocking, 11 non-blocking)?", Recommend: "request-changes",
						Options: []record.Option{
							{ID: "request-changes", Label: "Publish REQUEST_CHANGES", Consequence: "Publishes the draft as is, against the human approvals.", Publishes: true},
							{ID: "comment", Label: "Publish as COMMENT", Consequence: "Same comments, without blocking the merge.", Publishes: true},
							{ID: "hold", Label: "Do not publish", Consequence: "Nothing reaches GitHub."},
						}}}},
				{ID: 38, TurnSeq: 26, At: at(14, 32), Record: record.Record{State: "done", Summary: "REQUEST_CHANGES published",
					Outcome: &record.Outcome{Verdict: "REQUEST_CHANGES", URL: "https://github.com/acme/docs/pull/407#pullrequestreview-1", Blocking: 5, NonBlocking: 11}}},
			},
			Answers: []dturn.Answer{
				{ID: 41, TurnSeq: 26, Text: "Publish REQUEST_CHANGES (option request-changes)", OptionID: "request-changes", Source: dturn.Cockpit, Route: dturn.Typed, Session: "term_6ed72265", At: at(14, 31)},
			},
		},
		{
			Turn: dturn.Turn{Seq: 27, Intent: "review", ID: "bd39f565639b", Trigger: dturn.Automated, State: dturn.Asked, Session: "term_6ed72265", Agent: "claude", SentAt: at(23, 55), StartedAt: time.Date(2026, 10, 20, 0, 1, 0, 0, time.UTC)},
			Records: []dturn.Record{{ID: 39, TurnSeq: 27, At: time.Date(2026, 10, 20, 0, 8, 0, 0, time.UTC), Record: record.Record{State: "asked", Summary: "The 5 blocking issues are fixed; approve?",
				Question: &record.Question{Kind: "confirm", Text: "Approve with no body and resolve the threads?", Recommend: "yes", Publishes: true}}}},
			Answers: []dturn.Answer{{ID: 43, TurnSeq: 27, Yes: true, Source: dturn.Notification, Route: dturn.Refused, At: time.Date(2026, 10, 20, 0, 9, 0, 0, time.UTC)}},
		},
	}}
}

func TestTurnShowReadsAsABookOnATerminal(t *testing.T) {
	var out bytes.Buffer
	writeBook(&out, termfmt.Term{TTY: true, Color: true, Width: 100}, bookFixture(), time.UTC)
	golden(t, "turn_show_tty.golden", bracketed(out.String()))
}

func TestTurnShowStaysJSONWhenPiped(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.gh.basics = openBasics
	ctx := context.Background()
	if err := te.Store.UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h1", Reasons: []string{"repo"}, URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	agentDaemon(t, te)
	inSession(te, "term_1")
	if code := te.run("instructions", "nudge", r7.URL(), "--", "be", "brief"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	book, err := client.New(te.Socket).TurnBook(context.Background(), pr.Ref(r7))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(book, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := string(raw) + "\n"
	for _, c := range []struct {
		env  map[string]string
		args []string
	}{
		{nil, nil},
		{map[string]string{"CLICOLOR_FORCE": "1"}, nil},
		{map[string]string{"GH_FORCE_TTY": "1"}, []string{"--json"}},
		{nil, []string{"--color", "always", "--json"}},
	} {
		te.out.Reset()
		te.Getenv = func(k string) string { return c.env[k] }
		if code := te.run(append([]string{"turn", "show", r7.URL()}, c.args...)...); code != 0 {
			t.Fatalf("%v %v: exit %d: %s", c.env, c.args, code, te.err)
		}
		if got := te.out.String(); got != want {
			t.Errorf("%v %v: not the JSON book:\n%s", c.env, c.args, got)
		}
	}
	te.out.Reset()
	te.Getenv = func(k string) string { return map[string]string{"GH_FORCE_TTY": "100"}[k] }
	if code := te.run("turn", "show", r7.URL()); code != 0 || strings.HasPrefix(te.out.String(), "{") || !strings.Contains(te.out.String(), "#1") {
		t.Fatalf("exit %d, not a book on a terminal:\n%s%s", code, te.out, te.err)
	}
}

func TestTurnShowFitsANarrowTerminal(t *testing.T) {
	b := bookFixture()
	b.Turns[0].Records[1].Record.Question.Options = append(b.Turns[0].Records[1].Record.Question.Options,
		record.Option{ID: "a-very-long-option-identifier-past-the-cap", Label: "Something else", Consequence: "Takes the long road."})
	for _, width := range []int{40, 60, 80, 100} {
		var out bytes.Buffer
		writeBook(&out, termfmt.Term{TTY: true, Color: true, Width: width}, b, time.UTC)
		fits(t, "turn show", out.String(), width)
		if !strings.Contains(out.String(), "recommended") {
			t.Fatalf("at %d the recommended option is not marked:\n%s", width, out.String())
		}
	}
}
