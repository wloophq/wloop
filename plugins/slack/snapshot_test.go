package slack

import (
	"context"
	"encoding/json"
	"github.com/wloophq/wloop/plugins/slack/store"
	"slices"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

func TestSlackIdentityMatchesTheViewer(t *testing.T) {
	f := newFixture(t)
	if _, err := f.p.Identity(context.Background()); err == nil {
		t.Fatal("an identity before any login")
	}
	f.login(t)
	f.people(t)
	id, err := f.p.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	who := host.PersonOf(id.Viewer)
	if who.Key != "slack:"+me || who.Name != "Fer" || !slices.Equal(who.Aliases, []string{"email:me@example.test"}) {
		t.Fatalf("identity %+v", who)
	}
	ident := items.Identity{Keys: append([]string{who.Key}, who.Aliases...)}
	assignee := items.Person{Key: "slack:" + me, Name: "Fer"}
	if !assignee.Is(ident) || otherPerson.Is(ident) {
		t.Fatalf("Is: viewer %v other %v", assignee.Is(ident), otherPerson.Is(ident))
	}
	if byEmail := (items.Person{Key: "github:fer", Aliases: []string{"email:me@example.test"}}); !byEmail.Is(ident) {
		t.Fatal("the email alias does not match across plugins")
	}
}

// report sends the host what the store's commits and the host's notes changed.
func (f *fixture) report(t testing.TB) {
	t.Helper()
	if err := f.p.reports.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// reported is u's latest snapshot the host holds.
func (f *fixture) reported(t testing.TB, u unit.Unit) src.Snapshot {
	t.Helper()
	s, ok := f.host.Snapshot(u)
	if !ok {
		t.Fatalf("no snapshot of %s; reported %+v", u, f.host.Snapshots())
	}
	return s
}

func scriptedItem(ref, origin, requester string, waiting []string, state string) map[string]any {
	res, ev, kind := "none", "", "none"
	if state == "resolved" {
		res, ev, kind = "done", ts(3), "message"
	}
	people := make([]any, len(waiting))
	for i, id := range waiting {
		people[i] = answerPerson(id)
	}
	return map[string]any{"ref": ref, "kind": "request", "summary": "item " + ref, "origin_ts": origin, "requester": answerPerson(requester),
		"assignees": people, "assignment_ts": origin, "assignment_basis": "explicit_mention", "waiting_on": people,
		"state": state, "resolution": res, "evidence_ts": ev, "evidence_kind": kind, "confidence": "high", "ambiguity": "",
		"links": []any{}, "context": "", "options": []any{}, "deadline": "", "mode": "none",
		"place": map[string]string{"context": "", "why": ""}}
}

func TestSlackItemsReachTheSnapshot(t *testing.T) {
	f := newFixture(t)
	answer, err := json.Marshal(map[string]any{"brief": json.RawMessage(`{"unchanged": false, "goal": "b", "context": "", "kind": "request", "risk": "low", "risk_why": "", "open_questions": [], "tags": {"domain": [], "audience": [], "concern": []}}`), "viewer_role": "assignee", "started_by_viewer": false,
		"items": []any{
			scriptedItem("n1", ts(1), other, []string{me}, "open"),
			scriptedItem("n2", ts(2), me, []string{other}, "open"),
			scriptedItem("n3", ts(1), other, []string{me}, "resolved"),
		},
		"verdict": "waits_on_viewer", "verdict_why": "Ana waits on you", "closure_posted": false, "segments": []any{}, "links": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	f.answers(func(string) []byte { return answer })
	f.login(t)
	f.people(t)
	f.now = t0.Add(-time.Hour)
	u := f.thread(t)
	f.now = t0
	if _, _, err := f.p.job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	its := f.host.Items(u)
	if len(its) != 3 {
		t.Fatalf("items %+v", its)
	}
	ids := map[string]string{}
	for _, it := range its {
		ids[it.Summary] = it.ID
	}
	f.report(t)
	s := f.reported(t, u)
	if c := s.Card; c == nil || c.State != "waits_on_viewer" || len(c.WaitsOn) != 2 {
		t.Fatalf("card %+v", s.Card)
	}
	if s.Situation == nil || s.Situation.Kind != "waits_on_viewer" {
		t.Errorf("situation %+v", s.Situation)
	}
	waits := map[string]string{}
	for _, it := range s.Items {
		if len(it.WaitingOn) != 1 {
			t.Fatalf("item %+v", it)
		}
		waits[it.ID] = it.WaitingOn[0].Key
	}
	if len(waits) != 2 || waits[ids["item n1"]] != mePerson.Key || waits[ids["item n2"]] != otherPerson.Key {
		t.Errorf("open items of the snapshot: %+v", waits)
	}
}

func TestNewReplyReportsTheUnitAgain(t *testing.T) {
	f := newFixture(t)
	f.now = tsAt(4)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	f.state(t, u, store.ExtractionState{Verdict: "waits_on_others", VerdictWhy: "Ana owes the doc", StartedByViewer: true, At: f.now})
	applyOps(t, f.ledger(), items.Extracted, createOp(u, "e1", 2, mePerson, otherPerson))
	f.report(t)
	first := f.reported(t, u)
	if first.Card == nil || first.Card.State != "waits_on_others" || first.Card.LastHuman == nil {
		t.Fatalf("first card %+v", first.Card)
	}
	f.report(t)
	if n := len(f.host.Reports()); n != 1 {
		t.Fatalf("an unchanged unit was reported again: %d reports", n)
	}

	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(10), ReplyCount: 3, Messages: []dslack.Message{
		{TS: ts(10), ThreadTS: ts(1), User: other, Text: "here is the doc"}}}}})
	f.report(t)
	c := f.reported(t, u).Card
	if c == nil || c.LastHuman == nil || !c.LastHuman.At.After(first.Card.LastHuman.At) || c.LastHuman.What != "here is the doc" {
		t.Fatalf("after the reply %+v", c)
	}
}

func TestUnconfiguredSlackServesNoIdentityNorUnits(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	if _, err := f.p.Identity(context.Background()); err != nil {
		t.Fatalf("a configured Slack lacks its identity: %v", err)
	}
	f.report(t)
	f.reported(t, u)
	f.cfg.Slack.ClientID = ""
	f.build(t)
	if _, err := f.p.Identity(context.Background()); err == nil {
		t.Error("an unconfigured Slack serves an identity")
	}
	f.report(t)
	if s, ok := f.host.Snapshot(u); ok {
		t.Errorf("an unconfigured Slack reports %+v", s)
	}
}
