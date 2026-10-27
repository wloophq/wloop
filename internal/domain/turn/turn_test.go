package turn

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	r7 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
)

func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

func askedRecord(publishes bool) record.Record {
	return record.Record{State: record.Asked, Summary: "publish?", Resume: "draft final", Outcome: &record.Outcome{Verdict: "APPROVE"},
		Question: &record.Question{Kind: "choice", Text: "q", Options: []record.Option{{ID: "publish", Label: "Publish", Consequence: "c", Publishes: publishes}, {ID: "hold", Label: "Hold", Consequence: "c"}}}}
}

var approved = record.Record{State: record.Done, Summary: "approved", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "u"}}

func TestABookFromSendToEnd(t *testing.T) {
	b := Book{Unit: r7}
	b.Send(Turn{Intent: "review", ID: "abc", Trigger: "automated", Head: "h1", SentAt: at(0)}).Seq = 1
	// The book's pointers do not survive an append: read the turn afresh.
	first := func() *Turn { t, _ := b.ByID("abc"); return t }
	if _, err := b.Start("abc", "term_1", at(1)); err != nil || first().State != Started || first().Session != "term_1" {
		t.Fatalf("%+v %v", first(), err)
	}
	if !b.OwesRecord(1) {
		t.Fatal("a started turn owes a record")
	}
	rec := askedRecord(true)
	if !b.Record(Record{TurnSeq: 1, Session: "term_1", Hash: rec.Hash(), At: at(2), Record: rec}) || first().State != Asked {
		t.Fatalf("%+v", first())
	}
	if b.Record(Record{TurnSeq: 1, Session: "term_1", Hash: rec.Hash(), At: at(3), Record: rec}) {
		t.Fatal("the same record twice")
	}
	if b.OwesRecord(1) || !b.OpenQuestion("review") {
		t.Fatal("asked: no record owed, the question is open")
	}
	b.Send(Turn{Intent: "nudge", ID: "n1", Trigger: "by-hand", SentAt: at(4)})
	if first().State != Asked {
		t.Fatal("a nudge replaced a review turn")
	}
	b.Send(Turn{Intent: "review", ID: "def", Trigger: "by-hand", SentAt: at(5)})
	if first().State != Replaced {
		t.Fatalf("%+v", first())
	}
	b.Record(Record{TurnSeq: 1, Session: "term_1", Hash: approved.Hash(), At: at(6), Record: approved})
	if first().State != Replaced {
		t.Fatal("a late record revived a replaced turn")
	}
}

func TestOnlyARetryOfTheLastRecordIsDropped(t *testing.T) {
	rec := askedRecord(false)
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", ID: "abc", State: Started, Session: "term_1", StartedAt: at(1)}}}
	add := func(id int64, m int) bool {
		added := b.Record(Record{TurnSeq: 1, Session: "term_1", Hash: b.RecordHash(1, rec), At: at(m), Record: rec})
		if added {
			b.Records[len(b.Records)-1].ID = id
		}
		return added
	}
	if !add(1, 2) {
		t.Fatal("the first record was dropped")
	}
	if b.Records[0].Hash != rec.Hash() {
		t.Fatal("the first record of a turn is not stored under its content hash")
	}
	if add(0, 3) {
		t.Fatal("a retry of the last record was stored")
	}
	b.Answer(Answer{TurnSeq: 1, Text: "go", Source: Cockpit, At: at(4)}).Route = Typed
	if !add(2, 5) {
		t.Fatal("an identical re-ask after an answer was dropped")
	}
	if add(0, 6) {
		t.Fatal("a retry of the re-ask was stored")
	}
}

func TestOwesRecord(t *testing.T) {
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", ID: "abc", State: Asked, Session: "term_1", StartedAt: at(1)}},
		Records: []Record{{ID: 1, TurnSeq: 1, Session: "term_1", At: at(2), Record: askedRecord(false)}}}
	refused := b.Answer(Answer{TurnSeq: 1, Text: "go", Source: Cockpit, At: at(3)})
	refused.Route = Refused
	if b.OwesRecord(1) {
		t.Fatal("a refused answer made the stop owe a record (F8)")
	}
	if _, ok := b.AnsweredAt(1); ok {
		t.Fatal("a refused answer reads as sent (F8)")
	}
	typed := b.Answer(Answer{TurnSeq: 1, Text: "go", Source: Cockpit, At: at(4)})
	typed.Route, typed.Session = Typed, "term_1"
	if !b.OwesRecord(1) {
		t.Fatal("an answer typed into the session makes its next stop owe a record")
	}
}

func TestAnAnswerNoticedThroughTheStopHookCountsAsDelivered(t *testing.T) {
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", ID: "abc", State: Asked, Session: "term_1", StartedAt: at(1)}},
		Records: []Record{{ID: 1, TurnSeq: 1, Session: "term_1", At: at(2), Record: askedRecord(false)}}}
	hooked := b.Answer(Answer{TurnSeq: 1, Text: "go", Source: Cockpit, At: at(3)})
	hooked.Route, hooked.Session = Hooked, "term_1"
	if !b.OwesRecord(1) {
		t.Fatal("an answer noticed through the Stop hook does not make the turn owe a record")
	}
	if got, ok := b.AnsweredAt(1); !ok || !got.Equal(at(3)) {
		t.Fatalf("answered at %v %v", got, ok)
	}
	if h := b.History(10); len(h) != 1 || len(h[0].Said) != 1 || h[0].Said[0] != "go" {
		t.Fatalf("history %+v", h)
	}
}

func TestQuestionIsTheOpenOneAmongTheAnswerable(t *testing.T) {
	answerable := []string{"review", "address", "probe"}
	b := Book{Unit: r7, Turns: []Turn{
		{Seq: 1, Intent: Intent(dintent.Review), ID: "r", State: Asked},
		{Seq: 2, Intent: Intent(dintent.Address), ID: "a", State: Done},
		{Seq: 3, Intent: Intent(dintent.Nudge), ID: "n", State: Asked},
	}}
	if q, ok := b.Question(answerable); !ok || q.Seq != 1 {
		t.Fatalf("question %+v: an asked review hides behind a done address turn", q)
	}
	b.Turns[0].State = Done
	if q, ok := b.Question(answerable); !ok || q.Seq != 2 {
		t.Fatalf("question %+v: with none open, the newest turn of the intents", q)
	}
	b.Turns = append(b.Turns, Turn{Seq: 4, Intent: Intent(dintent.Address), ID: "a2", State: Failed}, Turn{Seq: 5, Intent: Intent(dintent.Review), ID: "r2", State: Asked})
	if q, ok := b.Question(answerable); !ok || q.Seq != 5 {
		t.Fatalf("question %+v: the newest open one", q)
	}
	if q, ok := b.Question([]string{"nudge", "ask-author"}); !ok || q.Seq != 3 {
		t.Fatalf("question %+v: the set decides which intents count", q)
	}
	if _, ok := b.Question(nil); ok {
		t.Fatal("no intent is answerable, so no turn is a question")
	}
}

func TestOpenQuestion(t *testing.T) {
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "address", State: Asked}}, Answers: []Answer{{TurnSeq: 1, Route: Typed, At: at(5)}}}
	if !b.OpenQuestion("address") {
		t.Fatal("an answered question still holds while the turn is asked (F7)")
	}
	b.Turns[0].State = Failed
	if b.OpenQuestion("address") {
		t.Fatal("a stopped turn holds rounds")
	}
}

func TestEndorsedBelongsToTheVerdict(t *testing.T) {
	silent := record.Record{State: record.Asked, Summary: "s", Resume: "r", Question: &record.Question{Kind: "choice", Text: "q", Options: []record.Option{{ID: "nudge", Label: "N", Consequence: "c"}, {ID: "drop", Label: "D", Consequence: "c"}}}}
	for _, c := range []struct {
		name    string
		trigger Trigger
		asked   record.Record
		answers []Answer
		resume  bool
		want    string
	}{
		{"by hand", "by-hand", askedRecord(true), nil, false, "yes: by hand"},
		{"by hand, resumed", "by-hand", askedRecord(true), nil, true, "yes: by hand"},
		{"chose publish", "automated", askedRecord(true), []Answer{{TurnSeq: 1, OptionID: "publish", Source: Cockpit, Route: Typed}}, false, "yes: the user approved"},
		{"chose publish, then resumed (F9)", "automated", askedRecord(true), []Answer{{TurnSeq: 1, OptionID: "publish", Source: Notification, Route: Resumed}}, true, "yes: the user approved"},
		{"chose hold, published anyway", "automated", askedRecord(true), []Answer{{TurnSeq: 1, OptionID: "hold", Source: Cockpit, Route: Typed}}, false, "no"},
		{"silent author nudge", "automated", silent, []Answer{{TurnSeq: 1, OptionID: "nudge", Source: Notification, Route: Typed}}, false, "no"},
		{"free text", "automated", askedRecord(true), []Answer{{TurnSeq: 1, Text: "go ahead", Source: Cockpit, Route: Typed}}, false, "no"},
		{"terminal", "automated", askedRecord(true), []Answer{{TurnSeq: 1, Text: "publish", Source: Terminal, Route: Direct}}, false, "no"},
		{"continue only", "automated", askedRecord(true), nil, true, "no"},
	} {
		for i := range c.answers {
			c.answers[i].At = at(2) // after the asked record, before the done one
		}
		b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", Trigger: c.trigger, State: Asked}},
			Records: []Record{{TurnSeq: 1, At: at(1), Record: c.asked}}, Answers: c.answers}
		done := int64(1)
		if c.resume {
			b.Turns = append(b.Turns, Turn{Seq: 2, Intent: "review", Trigger: c.trigger, ResumeOf: 1, State: Done})
			done = 2
		}
		b.Records = append(b.Records, Record{TurnSeq: done, At: at(3), Record: approved})
		if got := b.Endorsed(done); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAnAnswerEndorsesOnlyTheQuestionItAnswered(t *testing.T) {
	confirm := func(publishes bool) record.Record {
		return record.Record{State: record.Asked, Summary: "s", Resume: "r", Question: &record.Question{Kind: "confirm", Text: "q", Publishes: publishes}}
	}
	choice := func(publishes bool) record.Record {
		return record.Record{State: record.Asked, Summary: "s", Resume: "r", Question: &record.Question{Kind: "choice", Text: "q",
			Options: []record.Option{{ID: "go", Label: "Go", Consequence: "c", Publishes: publishes}, {ID: "hold", Label: "Hold", Consequence: "c"}}}}
	}
	for _, c := range []struct {
		name   string
		q1, q2 record.Record
		a1, a2 Answer
	}{
		{"no to a publishing confirm, then yes to one that does not publish", confirm(true), confirm(false),
			Answer{Yes: false}, Answer{Yes: true}},
		{"an option id reused by a question that does not publish", choice(true), choice(false),
			Answer{OptionID: "hold"}, Answer{OptionID: "go"}},
	} {
		c.a1.TurnSeq, c.a1.Source, c.a1.Route, c.a1.At = 1, Cockpit, Typed, at(2)
		c.a2.TurnSeq, c.a2.Source, c.a2.Route, c.a2.At = 1, Cockpit, Typed, at(4)
		b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", Trigger: "automated", State: Done}},
			Records: []Record{{TurnSeq: 1, At: at(1), Record: c.q1}, {TurnSeq: 1, At: at(3), Record: c.q2}, {TurnSeq: 1, At: at(5), Record: approved}},
			Answers: []Answer{c.a1, c.a2}}
		if got := b.Endorsed(1); got != "no" {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", Trigger: "by-hand", State: Asked}}, Records: []Record{{TurnSeq: 1, At: at(1), Record: askedRecord(true)}}}
	if got := b.Endorsed(1); got != "no" {
		t.Errorf("a by-hand turn that published nothing: %q", got)
	}
}

func TestCapable(t *testing.T) {
	for _, c := range []struct {
		name string
		s    Session
		want bool
	}{
		{"never initialized", Session{StartedAt: at(0)}, false},
		{"initialized at its start", Session{StartedAt: at(0), MCPAt: at(0).Add(2 * time.Second)}, true},
		{"initialized just before the start hook", Session{StartedAt: at(0), MCPAt: at(0).Add(-10 * time.Second)}, true},
		{"a later start without MCP", Session{StartedAt: at(10), MCPAt: at(0)}, false},
		{"start never reported", Session{MCPAt: at(0)}, true},
	} {
		if got := c.s.Capable(); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestDecideStop(t *testing.T) {
	for _, c := range []struct {
		f    StopFacts
		want StopAction
	}{
		{StopFacts{}, Allow},
		{StopFacts{HasTurn: true, Unacked: true, Owes: true}, AckAndAllow},
		{StopFacts{HasTurn: true}, LogAndAllow},
		{StopFacts{HasTurn: true, Owes: true}, Block},
		{StopFacts{HasTurn: true, Owes: true, BlockedThisPrompt: true}, StoreUnrecorded},
	} {
		if got := DecideStop(c.f); got != c.want {
			t.Errorf("%+v: %v, want %v", c.f, got, c.want)
		}
	}
}

func TestHistory(t *testing.T) {
	triage := record.Record{State: record.Done, Summary: "fixed two", Next: "watch CI", Artifact: &record.Artifact{Kind: "triage", Body: strings.Repeat("x", 2000)}}
	b := Book{Unit: r7,
		Turns: []Turn{
			{Seq: 1, Intent: "review", Trigger: "by-hand", Head: "h0", State: Asked, SentAt: at(0)},
			{Seq: 2, Intent: "review", Trigger: "by-hand", Head: "h1", ResumeOf: 1, State: Done, SentAt: at(10), Guidance: "be brief"},
			{Seq: 3, Intent: "address", Trigger: "automated", Head: "h2", State: Done, SentAt: at(20)},
			{Seq: 4, Intent: "address", Trigger: "automated", Head: "h3", State: Failed, SentAt: at(30)},
		},
		Records: []Record{
			{TurnSeq: 1, At: at(1), Record: askedRecord(true)},
			{TurnSeq: 2, Head: "h1r", At: at(11), Record: approved},
			{TurnSeq: 3, At: at(21), Record: triage},
			{TurnSeq: 4, At: at(31), Record: record.Record{State: record.Failed, Summary: "broke", Resume: "r", Artifact: &record.Artifact{Kind: "triage", Body: "t"}}},
		},
		HistoryArtifact: func(intent string) string {
			if intent == "address" {
				return "triage"
			}
			return ""
		},
		Answers: []Answer{
			{TurnSeq: 1, OptionID: "publish", Route: Resumed, At: at(2)},
			{TurnSeq: 2, Text: "ship it", Route: Typed, At: at(12)},
			{TurnSeq: 2, Yes: true, Text: "and say thanks", Route: Typed, At: at(13)},
			{TurnSeq: 2, No: true, Text: "keep the draft", Route: Typed, At: at(13)},
			{TurnSeq: 2, Text: "busy", Route: Refused, At: at(14)},
			{TurnSeq: 2, Text: "never routed", Route: Pending, At: at(15)},
			{TurnSeq: 2, Text: "waits for the stop", Route: Queued, At: at(16)},
		},
	}
	h := b.History(3)
	if len(h) != 3 || h[0].Intent != "address" || h[0].State != "failed" || h[2].Intent != "review" || h[2].Head != "h1r" {
		t.Fatalf("%+v", h)
	}
	if h[0].Triage != "" || h[0].Head != "h3" {
		t.Fatalf("a failed address turn: %+v", h[0])
	}
	if n := len([]rune(h[1].Triage)); n != 1500 || h[1].Summary != "fixed two" || h[1].Next != "watch CI" || h[1].Endorsed != "" {
		t.Fatalf("done address: %d %+v", n, h[1])
	}
	if e := h[2]; e.Verdict != "APPROVE" || e.Endorsed != "yes: by hand" || e.Trigger != "by-hand" || !e.When.Equal(at(10)) ||
		!slices.Equal(e.Said, []string{"ship it", "yes. and say thanks", "no. keep the draft", "be brief"}) {
		t.Fatalf("review: %+v", e)
	}
	if all := b.History(10); len(all) != 4 || !slices.Equal(all[3].Said, []string{"Publish (option publish)"}) || all[3].Verdict != "" || all[3].Endorsed != "" {
		t.Fatalf("%+v", all)
	}
}

func TestSaid(t *testing.T) {
	b := Book{Unit: r7, Records: []Record{{TurnSeq: 1, At: at(1), Record: askedRecord(true)}}}
	for _, c := range []struct {
		a    Answer
		want string
	}{
		{Answer{Text: "keep the column"}, "keep the column"},
		{Answer{Yes: true}, "yes"},
		{Answer{No: true}, "no"},
		{Answer{}, "no"},
		{Answer{Yes: true, Text: " but say thanks "}, "yes. but say thanks"},
		{Answer{No: true, Text: "the draft is wrong"}, "no. the draft is wrong"},
		{Answer{OptionID: "publish"}, "Publish (option publish)"},
		{Answer{OptionID: "publish", Text: "Publish (option publish)"}, "Publish (option publish)"},
		{Answer{OptionID: "publish", Text: "shorten the summary"}, "Publish (option publish). shorten the summary"},
	} {
		c.a.TurnSeq = 1
		if got := b.Said(c.a); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a, got, c.want)
		}
	}
}

func TestStartAndDismiss(t *testing.T) {
	b := Book{Unit: r7}
	if _, err := b.Start("abc", "term_1", at(0)); !errors.Is(err, ErrNoTurn) {
		t.Fatal(err)
	}
	b.Send(Turn{Seq: 1, Intent: "address", ID: "abc", Trigger: "automated", SentTo: "term_0", SentAt: at(0)})
	if tr, err := b.Start("abc", "term_1", at(1)); err != nil || tr.Session != "term_1" || !tr.StartedAt.Equal(at(1)) {
		t.Fatalf("a turn sent to another handle starts on the caller: %+v %v", tr, err)
	}
	if tr, err := b.Start("abc", "term_1", at(2)); err != nil || !tr.StartedAt.Equal(at(1)) {
		t.Fatalf("started twice: %+v %v", tr, err)
	}
	if _, err := b.Start("abc", "term_2", at(2)); !errors.Is(err, ErrOtherSession) {
		t.Fatal(err)
	}
	if tr, ok := b.OfSession("term_1"); !ok || tr.Seq != 1 {
		t.Fatal("of session")
	}
	if _, err := b.Dismiss(at(3), []string{"review", "address", "probe"}); !errors.Is(err, ErrClosed) {
		t.Fatal("a started turn is not dismissed")
	}
	b.Record(Record{TurnSeq: 1, Hash: "x", At: at(4), Record: record.Record{State: record.Failed, Summary: "s", Resume: "r", User: "stop"}})
	if a := b.Answers[0]; a.Route != Direct || a.Source != Terminal || a.Text != "stop" || !b.Turns[0].EndedAt.Equal(at(4)) {
		t.Fatalf("%+v %+v", a, b.Turns[0])
	}
	if b.Record(Record{TurnSeq: 1, Hash: "y", At: at(4), Record: askedRecord(false)}); b.Turns[0].State != Asked || !b.Turns[0].EndedAt.IsZero() {
		t.Fatalf("a record that reopens the turn: %+v", b.Turns[0])
	}
	b.Send(Turn{Seq: 2, Intent: "nudge", ID: "n", SentAt: at(5)})
	if tr, err := b.Dismiss(at(6), []string{"review", "address", "probe"}); err != nil || tr.Seq != 1 || tr.State != Dismissed {
		t.Fatalf("%+v %v", tr, err)
	}
	if _, err := b.Start("abc", "term_1", at(7)); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	b.Send(Turn{Seq: 3, Intent: "address", ID: "r2", ResumeOf: 1, Trigger: "by-hand", SentAt: at(8)})
	if b.Turns[2].Trigger != "automated" {
		t.Fatal("a resume turn takes the resumed turn's trigger")
	}
	if c := b.Chain(3); len(c) != 2 || c[0].Seq != 3 || c[1].Seq != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestWithdrawUndoesASendThatReplacedAQuestion(t *testing.T) {
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: Intent(dintent.Review), ID: "q", State: Asked, Session: "term_1", SentAt: at(0)}}}
	b.Records = []Record{{ID: 1, TurnSeq: 1, Hash: "h", At: at(1), Record: record.Record{State: record.Asked, Summary: "publish?"}}}
	b.Send(Turn{Intent: Intent(dintent.Review), ID: "r", ResumeOf: 1, SentAt: at(2)})
	b.Turns[1].Seq = 2
	b.Turns[0].ReplacedBy = 2
	if old := b.Turns[0]; old.State != Replaced || old.Before != Asked || b.OpenQuestion(Intent(dintent.Review)) {
		t.Fatalf("after the send: %+v", b.Turns)
	}
	if _, err := b.Withdraw(2, at(3)); err != nil {
		t.Fatal(err)
	}
	old := b.Turns[0]
	if old.State != Asked || old.Before != "" || old.ReplacedBy != 0 || !old.EndedAt.IsZero() || b.Turns[1].State != Withdrawn {
		t.Fatalf("after the withdraw: %+v", b.Turns)
	}
	if q, ok := b.Question([]string{"review", "address", "probe"}); !ok || q.Seq != 1 || !b.OpenQuestion(Intent(dintent.Review)) {
		t.Fatalf("question %+v %v", q, ok)
	}
	if h := b.History(10); len(h) != 1 || h[0].State != string(Asked) {
		t.Fatalf("history %+v", h)
	}
	if _, ok := b.ByID("r"); ok {
		t.Fatal("the withdrawn round is found")
	}
	if _, err := b.Withdraw(2, at(4)); !errors.Is(err, ErrClosed) {
		t.Fatalf("a second withdraw: %v", err)
	}
	if b.Record(Record{TurnSeq: 2, Hash: "late", At: at(5), Record: record.Record{State: record.Done, Summary: "x"}}); b.Turns[1].State != Withdrawn {
		t.Fatalf("a late record moved the withdrawn turn: %+v", b.Turns[1])
	}
}
