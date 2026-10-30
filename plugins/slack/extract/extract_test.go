package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/link"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

var threadUnit = unit.Unit{Provider: slack.Provider, Key: "T0001/C0001/1700000000.000100"}

type fixture struct {
	Conversation slack.Conversation
	Users        []slack.User
	Messages     []slack.Message
}

func loadInput(t *testing.T) Input {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "thread.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	users := map[string]slack.User{}
	for _, u := range f.Users {
		users[u.ID] = u
	}
	return Input{
		Prompts:      testPrompts,
		Unit:         threadUnit,
		Viewer:       Viewer{ID: "U0000000001", Name: "Ann"},
		Now:          time.Date(2023, 11, 15, 9, 0, 0, 0, time.FixedZone("BRT", -3*3600)),
		Language:     "English",
		Conversation: f.Conversation,
		Messages:     f.Messages,
		NewFrom:      "1700003600.000300",
		Users:        users,
		Ledger: []items.Item{
			{ID: "i1", Unit: threadUnit, Kind: "request", Summary: "Bob reviews the release notes draft", State: "open",
				Requester: items.Person{Key: "slack:U0000000001", Name: "Ann"}, Assignees: []items.Person{{Key: "slack:U0000000002", Name: "Bob"}},
				Basis: "explicit_mention", WaitingOn: []items.Person{{Key: "slack:U0000000002", Name: "Bob"}}, Confidence: "high",
				Origin: unit.Anchor{Unit: threadUnit, Ref: "C0001/1700000000.000100"}},
			{ID: "i3", Unit: threadUnit, Kind: "decision", Summary: "Ship the widget toggle on by default?", State: "open",
				Requester: items.Person{Key: "slack:U0000000002", Name: "Bob"}, Assignees: []items.Person{{Key: "slack:U0000000003", Name: "Cy"}},
				Basis: "explicit_mention", Confidence: "high", Confirmed: true,
				Origin: unit.Anchor{Unit: threadUnit, Ref: "C0001/1700000300.000200"}},
		},
		Brief:    `{"goal":"Ann asked Bob to review the release notes."}`,
		Contexts: []string{"widget", "docs"},
	}
}

type scripted struct {
	answers [][]byte
	asks    []host.Ask
}

func (s *scripted) Ask(_ context.Context, a host.Ask) (host.Answer, error) {
	s.asks = append(s.asks, a)
	if len(s.answers) == 0 {
		return host.Answer{}, errors.New("no scripted answer left")
	}
	out := s.answers[0]
	s.answers = s.answers[1:]
	return host.Answer{JSON: out, Model: "scripted"}, nil
}

func answer(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func extractScripted(t *testing.T, in Input, name string) Result {
	t.Helper()
	r, _, err := Extract(context.Background(), &scripted{answers: [][]byte{answer(t, name)}}, host.Ask{Tier: host.TierFast}, in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var placeholders = []string{"{{VIEWER_ID}}", "{{VIEWER_NAME}}", "{{NOW}}", "{{LANGUAGE}}", "{{LEDGER}}", "{{CONTEXTS}}", "{{CONVERSATION}}"}

func TestPromptPlaceholders(t *testing.T) {
	for _, p := range placeholders {
		if n := strings.Count(promptTemplate, p); n != 1 {
			t.Errorf("%s appears %d times in the template", p, n)
		}
	}
	if n := len(strings.Fields(promptTemplate)); n > 5500 {
		t.Errorf("the template has %d words, over 5500", n)
	}
	in := loadInput(t)
	in.Messages[0].Text += " {{NOW}}"
	got, err := Prompt(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "{{") != 1 {
		t.Errorf("a placeholder is left in the prompt:\n%s", got)
	}
	for _, want := range []string{"Ann (user id U0000000001", "2023-11-15 09:00 BRT", "every text field in English", "- widget", "- docs"} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
}

var (
	slackIDPattern = regexp.MustCompile(`\b[UCWTBSGDAEF][A-Z0-9]*[0-9][A-Z0-9]*\b`)
	urlPattern     = regexp.MustCompile(`https?://([^/\s"'<>|)` + "`" + `]+)`)
)

func TestPromptHoldsNoRealData(t *testing.T) {
	for _, id := range slackIDPattern.FindAllString(promptTemplate, -1) {
		if !strings.HasPrefix(id, "U000") && !strings.HasPrefix(id, "C000") {
			t.Errorf("the template holds the Slack id %q", id)
		}
	}
	for _, m := range urlPattern.FindAllStringSubmatch(promptTemplate, -1) {
		if m[1] != "example.com" {
			t.Errorf("the template holds the URL host %q", m[1])
		}
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "*.json"))
	for _, f := range files {
		raw := answer(t, filepath.Base(f))
		for _, id := range slackIDPattern.FindAllString(string(raw), -1) {
			if !strings.HasPrefix(id, "U000") && !strings.HasPrefix(id, "C000") && !strings.HasPrefix(id, "T000") && !strings.HasPrefix(id, "B000") {
				t.Errorf("%s holds the Slack id %q", f, id)
			}
		}
		for _, m := range urlPattern.FindAllStringSubmatch(string(raw), -1) {
			if m[1] != "example.com" {
				t.Errorf("%s holds the URL host %q", f, m[1])
			}
		}
	}
}

func TestLedgerRendersRealIDs(t *testing.T) {
	in := loadInput(t)
	got, err := Prompt(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"i1 (open, request)", "i3 (open, decision, confirmed)", "Previous brief: Ann asked Bob"} {
		if !strings.Contains(got, want) {
			t.Errorf("the ledger lacks %q", want)
		}
	}
	if strings.Contains(got, "i2 (") {
		t.Error("the ledger renders i2, which it does not hold")
	}
	r := extractScripted(t, in, "answer.json")
	ops, _, err := Ops(in, r, "T0001", parseTarget)
	if err != nil {
		t.Fatal(err)
	}
	var i3 []items.OpKind
	for _, op := range ops {
		if op.Item == "i3" {
			i3 = append(i3, op.Op)
		}
	}
	if len(i3) == 0 || i3[len(i3)-1] != "resolve" {
		t.Errorf("ops on i3 = %v, want them to end with a resolve", i3)
	}
}

func TestWindowKeepsParentsAndNew(t *testing.T) {
	long := strings.Repeat("x", 400)
	in := Input{
		Prompts:      testPrompts,
		Unit:         unit.Unit{Provider: slack.Provider, Key: "T0001/C0002/seg/1"},
		Viewer:       Viewer{ID: "U0000000001", Name: "Ann"},
		Now:          time.Unix(1700100000, 0).UTC(),
		Conversation: slack.Conversation{ID: "C0002", Name: "general", Kind: "channel"},
		Messages: []slack.Message{
			{TS: "1700000001.000000", User: "U0000000001", Text: "lone old " + long},
			{TS: "1700000002.000000", ThreadTS: "1700000002.000000", User: "U0000000002", Text: "old parent " + long},
			{TS: "1700000003.000000", ThreadTS: "1700000002.000000", User: "U0000000001", Text: "old reply " + long},
			{TS: "1700000004.000000", User: "U0000000002", Text: "another lone old " + long},
			{TS: "1700000005.000000", ThreadTS: "1700000002.000000", User: "U0000000003", Text: "new reply " + long},
			{TS: "1700000006.000000", User: "U0000000003", Text: "new lone " + long},
		},
		NewFrom:     "1700000005.000000",
		WindowChars: 2000,
	}
	got := Render(in)
	if strings.Contains(got, "ts=1700000001.000000") {
		t.Error("the oldest message before the cursor was not cut")
	}
	for _, kept := range []string{"ts=1700000002.000000", "ts=1700000005.000000", "ts=1700000006.000000"} {
		if !strings.Contains(got, kept) {
			t.Errorf("the window lost %s:\n%s", kept, got)
		}
	}
	if !strings.Contains(got, "ts=1700000004.000000") {
		t.Errorf("the window cut more than it needed:\n%s", got)
	}
	if strings.Contains(got, "ts=1700000003.000000") {
		t.Error("the old reply, older than the kept lone message, was not cut")
	}
	in.WindowChars = 10
	got = Render(in)
	for _, kept := range []string{"ts=1700000002.000000", "ts=1700000005.000000", "ts=1700000006.000000"} {
		if !strings.Contains(got, kept) {
			t.Errorf("a tiny window lost %s", kept)
		}
	}
	if strings.Contains(got, "ts=1700000004.000000") || strings.Contains(got, "ts=1700000003.000000") {
		t.Error("a tiny window keeps an old message no new one needs")
	}
}

func TestRenderMarksNewAndThreads(t *testing.T) {
	in := loadInput(t)
	in.Messages[3].BotID, in.Messages[3].Username = "B0002", "agent-app"
	got := Render(in)
	lines := strings.Split(got, "\n")
	find := func(ts string) string {
		for _, l := range lines {
			if strings.Contains(l, "ts="+ts) {
				return l
			}
		}
		t.Fatalf("no line for %s:\n%s", ts, got)
		return ""
	}
	if l := find("1700000300.000200"); strings.Contains(l, " new ") || !strings.Contains(l, "reply to m1") || !strings.Contains(l, "<@U0000000003|Cy>") {
		t.Errorf("old reply line = %q", l)
	}
	if l := find("1700003600.000300"); !strings.Contains(l, "] new Bob (U0000000002)") {
		t.Errorf("new reply line = %q", l)
	}
	if !strings.Contains(got, "reactions: eyes by Cy (U0000000003)") {
		t.Errorf("reactions missing:\n%s", got)
	}
	if l := find("1700007200.000400"); !strings.Contains(l, "] new Cy (U0000000003) reply to m1") {
		t.Errorf("a human posting through an agent app is not its author: %q", l)
	}
	if l := find("1700010800.000500"); !strings.Contains(l, "ci-bot (bot B0001)") {
		t.Errorf("bot line = %q", l)
	}
}

func TestSchemaRoundTrip(t *testing.T) {
	var s map[string]any
	if err := json.Unmarshal([]byte(Schema()), &s); err != nil {
		t.Fatalf("schema.json: %v", err)
	}
	in := loadInput(t)
	a := &scripted{answers: [][]byte{answer(t, "answer.json")}}
	r, ans, err := Extract(context.Background(), a, host.Ask{Tier: host.TierFast, MaxCostUSD: 0.1}, in)
	if err != nil {
		t.Fatal(err)
	}
	if ans.Model != "scripted" || len(a.asks) != 1 || a.asks[0].Schema != Schema() || a.asks[0].MaxCostUSD != 0.1 || !strings.Contains(a.asks[0].Prompt, "ts=1700000000.000100") {
		t.Errorf("the ask was %+v", a.asks)
	}
	if r.Verdict != "waits_on_viewer" || !r.StartedByViewer || len(r.Items) != 3 || len(r.Links) != 1 {
		t.Fatalf("result = %+v", r)
	}
	n1 := r.Items[2]
	if n1.Ref != "n1" || n1.Mode != "session" || n1.Place.Context != "widget" || n1.Assignees[0] != "U0000000001" {
		t.Errorf("n1 = %+v", n1)
	}
	if r.Items[1].Options[1].BackedBy[0] != "U0000000003" || r.Links[0].Kind != link.Review {
		t.Errorf("result = %+v", r)
	}

	_, _, err = Extract(context.Background(), &scripted{answers: [][]byte{answer(t, "answer_bad_kind.json")}}, host.Ask{}, in)
	if !errors.Is(err, host.ErrUnfit) {
		t.Errorf("a link kind about: err = %v, want ErrUnfit", err)
	}
}

func TestUnfitAnswers(t *testing.T) {
	in := loadInput(t)
	good := string(answer(t, "answer.json"))
	for name, body := range map[string]string{
		"not json":         "{",
		"unknown field":    strings.Replace(good, `"brief":`, `"extra": 1, "brief":`, 1),
		"missing field":    strings.Replace(good, `"closure_posted": false,`, ``, 1),
		"unknown ref":      strings.Replace(good, `"ref": "i3"`, `"ref": "i2"`, 1),
		"duplicate ref":    strings.Replace(good, `"ref": "i3"`, `"ref": "i1"`, 1),
		"bad state":        strings.Replace(good, `"state": "open"`, `"state": "pending"`, 1),
		"link to no item":  strings.Replace(good, `{"item": "n1"`, `{"item": "n7"`, 1),
		"unknown segment":  strings.Replace(good, `"segments": []`, `"segments": [{"ref": "s1", "from_ts": "1700000000.000100", "to_ts": "", "topic": "x", "brief": {"unchanged": true, "goal": "", "context": "", "kind": "request", "risk": "low", "risk_why": "", "open_questions": [], "tags": {"domain": [], "audience": [], "concern": []}}}]`, 1),
		"origin not a ts":  strings.Replace(good, `"origin_ts": "1700007200.000400", "requester": "U0000000003"`, `"origin_ts": "2023-11-14 22:13", "requester": "U0000000003"`, 1),
		"ts with a space":  strings.Replace(good, `"evidence_ts": "1700003600.000300"`, `"evidence_ts": "1700003600.000300 "`, 1),
		"summary too long": strings.Replace(good, `"Ship the widget toggle on by default?"`, `"`+strings.Repeat("é", 201)+`"`, 1),
	} {
		_, _, err := Extract(context.Background(), &scripted{answers: [][]byte{[]byte(body)}}, host.Ask{}, in)
		if !errors.Is(err, host.ErrUnfit) {
			t.Errorf("%s: err = %v, want ErrUnfit", name, err)
		}
	}
	in.Contexts = nil
	r := extractScripted(t, in, "answer.json")
	if r.Items[2].Place.Context != "" {
		t.Errorf("a context not offered was kept: %+v", r.Items[2].Place)
	}
}

func TestAskErrorPassesThrough(t *testing.T) {
	_, _, err := Extract(context.Background(), &scripted{}, host.Ask{}, loadInput(t))
	if err == nil || errors.Is(err, host.ErrUnfit) {
		t.Errorf("err = %v", err)
	}
}

func BenchmarkExtractAndOps(b *testing.B) {
	in := loadInput(&testing.T{})
	parent := in.Messages[0]
	in.Messages = []slack.Message{parent}
	for i := 1; i < 2000; i++ {
		in.Messages = append(in.Messages, slack.Message{TS: fmt.Sprintf("1700%06d.000100", i), ThreadTS: parent.TS,
			User: "U0000000002", Text: strings.Repeat("a reply about the widget rollout ", 4), Mentions: []string{"U0000000001"}})
	}
	in.NewFrom = in.Messages[1900].TS
	in.WindowChars = 40000
	raw := answer(&testing.T{}, "answer.json")
	b.ResetTimer()
	for b.Loop() {
		r, _, err := Extract(context.Background(), &scripted{answers: [][]byte{raw}}, host.Ask{}, in)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := Ops(in, r, "T0001", parseTarget); err != nil {
			b.Fatal(err)
		}
	}
}
