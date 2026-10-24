package daemon

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

func draftQuestion(summary string) record.Record {
	return record.Record{State: record.Asked, Summary: summary,
		Outcome: &record.Outcome{Verdict: "APPROVE", Blocking: 0, NonBlocking: 2},
		Question: &record.Question{Kind: "choice", Text: "Publish the review?", Recommend: "publish", Options: []record.Option{
			{ID: "comment", Label: "Comment only", Consequence: "posts a comment"},
			{ID: "publish", Label: "Publish APPROVE", Consequence: "approves", Publishes: true},
			{ID: "hold", Label: "Hold the draft", Consequence: "nothing is posted"},
		}}}
}

// askTurn stores an asked review turn on n whose last record is rec.
func askTurn(t *testing.T, repo *sqlite.Store, n int, recs ...record.Record) {
	t.Helper()
	at := fixtureNow.Add(-30 * time.Minute)
	ref := pr.Ref(ref(n))
	b := turn.Book{Unit: ref.Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "r" + strconv.Itoa(n), Trigger: turn.Automated, Head: "h",
		Session: "term", State: turn.Asked, SentAt: at, StartedAt: at}}}
	for i, r := range recs {
		b.Records = append(b.Records, turn.Record{Session: "term", Hash: "x" + strconv.Itoa(i), At: at.Add(time.Duration(i) * time.Minute), Record: r})
	}
	if _, err := repo.Save(context.Background(), b, port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "r", At: at}); err != nil {
		t.Fatal(err)
	}
}

func addRecord(t *testing.T, repo *sqlite.Store, n int, r record.Record) {
	t.Helper()
	ctx := context.Background()
	books, err := repo.Books(ctx, pr.Ref(ref(n)).Unit())
	if err != nil {
		t.Fatal(err)
	}
	b := books[pr.Ref(ref(n)).Unit()]
	b.Record(turn.Record{TurnSeq: b.Turns[0].Seq, Session: "term", Hash: "again", At: fixtureNow.Add(-time.Minute), Record: r})
	if _, err := repo.Save(ctx, b, port.Event{Unit: b.Unit, Kind: turns.KindTurnEnded, Key: "again", At: fixtureNow}); err != nil {
		t.Fatal(err)
	}
}

// rowAttention is the notification the deriver makes of a needs-me row, which entry notifications are
// compared with.
func rowAttention(t *testing.T, s *Server, row api.Row) *api.Attention {
	t.Helper()
	if !slices.Contains(row.Views, api.ViewNeedsMe) {
		return nil
	}
	rd, ok, err := s.readRow(context.Background(), row.Unit)
	if err != nil || !ok {
		t.Fatalf("the row of %s: %v %v", row.Unit, ok, err)
	}
	return s.deriverAt(rd.Base, rd.At).Attention(rd.Row, rd.In)
}

func TestRowsCarryNoAttention(t *testing.T) {
	f := newFixture(t)
	repo := f.withTurns()
	f.open(30, "ana", fixtureNow.Add(-time.Hour), requests("fer"))
	askTurn(t, repo, 30, draftQuestion("the draft approves; publish it?"))
	if rowAttention(t, f.s, f.rows("?view=" + api.ViewNeedsMe)[0]) == nil {
		t.Fatal("the fixture's row would notify nothing")
	}
	if raw := getRaw(t, f.s, api.Rows); strings.Contains(raw, `"attention"`) || !strings.Contains(raw, "acme/api#30") {
		t.Fatalf("%s", raw)
	}
}

func actionsOf(a *api.Attention) []string {
	var out []string
	for _, x := range a.Actions {
		s := x.ID + " " + x.Title
		if x.TextInput {
			s += " [text]"
		}
		out = append(out, s)
	}
	return out
}

func TestAttentionForAQuestion(t *testing.T) {
	f := newFixture(t)
	repo := f.withTurns()
	f.open(30, "ana", fixtureNow.Add(-time.Hour), requests("fer"))
	askTurn(t, repo, 30, draftQuestion("the draft approves; publish it?"))
	rows := f.rows("?view=" + api.ViewNeedsMe)
	if len(rows) != 1 || rows[0].Question == nil || rowAttention(t, f.s, rows[0]) == nil {
		t.Fatalf("%+v", rows)
	}
	q, a := rows[0].Question, rowAttention(t, f.s, rows[0])
	if q.State != "asked" || q.Kind != "choice" || q.Verdict != "APPROVE" || q.NonBlocking != 2 || q.Recommend != "publish" || len(q.Options) != 3 || q.RecordID == 0 {
		t.Fatalf("question %+v", q)
	}
	want := []string{"answer:option:publish Publish APPROVE (recommended)", "answer:option:comment Comment only", "answer:option:hold Hold the draft",
		"answer:text Reply [text]", "continue Continue", "dismiss Dismiss", "pause Pause", "open Open"}
	if a.Category != "Q"+strconv.FormatInt(q.RecordID, 10) || a.Title != "Agent asks" || a.Subtitle != "acme/api #30" || !slices.Equal(actionsOf(a), want) {
		t.Fatalf("attention %+v %q", a, actionsOf(a))
	}
	if !strings.Contains(a.Body, "the draft approves; publish it?") || !strings.Contains(a.Body, "drafted APPROVE, 0 blocking, 2 non-blocking") ||
		!strings.Contains(a.Body, "recommends: Publish APPROVE") {
		t.Fatalf("body %q", a.Body)
	}

	addRecord(t, repo, 30, draftQuestion("the draft approves; publish it?"))
	again := f.rows("?view=" + api.ViewNeedsMe)[0]
	b := rowAttention(t, f.s, again)
	if again.Question.RecordID == q.RecordID || b.ID == a.ID || b.Category != "Q"+strconv.FormatInt(again.Question.RecordID, 10) {
		t.Fatalf("a new record with the same summary kept the item: %+v then %+v", a, b)
	}
}

func TestAttentionForAConfirmAndAStop(t *testing.T) {
	f := newFixture(t)
	repo := f.withTurns()
	f.open(31, "ana", fixtureNow.Add(-time.Hour), requests("fer"))
	f.open(32, "ana", fixtureNow.Add(-time.Hour), requests("fer"))
	askTurn(t, repo, 31, record.Record{State: record.Asked, Summary: "push the fix?",
		Question: &record.Question{Kind: "confirm", Text: "Push?", Recommend: "no"}})
	f.turn(repo, 32, turn.Intent(dintent.Review), turn.Unrecorded, "term-32")
	rows := f.rows("?view=" + api.ViewNeedsMe)
	byNumber := map[int]api.Row{}
	for _, r := range rows {
		byNumber[rowNumber(r)] = r
	}
	confirm := rowAttention(t, f.s, byNumber[31])
	if confirm == nil || !slices.Equal(actionsOf(confirm), []string{"answer:no No (recommended)", "answer:yes Yes", "answer:text Reply [text]", "continue Continue", "dismiss Dismiss", "pause Pause", "open Open"}) ||
		!strings.Contains(confirm.Body, "recommends: No") {
		t.Fatalf("confirm %+v", confirm)
	}
	stop := byNumber[32]
	sa := rowAttention(t, f.s, stop)
	if stop.Question == nil || stop.Question.State != "unrecorded" || sa == nil || sa.Title != "Agent stopped" ||
		!slices.Equal(actionsOf(sa), []string{"answer:text Reply [text]", "continue Continue", "dismiss Dismiss", "pause Pause", "open Open"}) {
		t.Fatalf("stop %+v %+v", stop.Question, sa)
	}
}

func TestAPermissionPromptOffersNoAnswer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	repo := f.withTurns(turns.AgentObservation{WorkspaceID: "w3", Pane: "t:1", State: turn.AgentWaiting})
	f.open(22, "fer", fixtureNow.Add(-time.Hour), requests("carla"))
	f.turn(repo, 22, turn.Intent(dintent.Address), turn.Started, "term-22")
	if err := repo.SaveSession(ctx, turn.Session{ID: "term-22", Pane: "t:1", WorkspaceID: "w3"}, port.Event{Kind: turns.KindSessionLaunched, Key: "term-22", At: fixtureNow}); err != nil {
		t.Fatal(err)
	}
	rows := f.rows("?view=" + api.ViewNeedsMe)
	if len(rows) != 1 || rows[0].Question != nil || rowAttention(t, f.s, rows[0]) == nil {
		t.Fatalf("%+v", rows)
	}
	if a := rowAttention(t, f.s, rows[0]); a.Title != "Waiting for a permission" || !slices.Equal(actionsOf(a), []string{"pause Pause", "open Open"}) {
		t.Fatalf("%+v", a)
	}
}

func TestOnlyNeedsMeRowsCarryAnAttentionItem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.open(40, "ana", fixtureNow.Add(-time.Hour), func(pr *dinv.PR) { pr.Reasons = nil })
	f.st.SetWorkspace(ctx, ref(40), dinv.Workspace{ID: "w40", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWaiting}})
	f.open(41, "ana", fixtureNow.Add(-time.Hour), func(pr *dinv.PR) { pr.Reasons = []string{"involved"} })
	f.st.SetOverride(ctx, dinv.Override{PR: ref(41), MutedUntil: fixtureNow.Add(time.Hour)})
	f.st.SetWorkspace(ctx, ref(41), dinv.Workspace{ID: "w41", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWaiting}})
	for _, r := range f.rows("?view=all") {
		inNeedsMe := slices.Contains(r.Views, api.ViewNeedsMe)
		if a := rowAttention(t, f.s, r); (a != nil) != inNeedsMe {
			t.Errorf("#%d views %v attention %+v", rowNumber(r), r.Views, a)
		}
	}
	if r := f.rows("?view=all"); len(r) != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestNudgeComputedOnce(t *testing.T) {
	f := newFixture(t)
	at := fixtureNow.Add(-time.Hour)
	f.open(50, "fer", at, requests("ana", "robo"))
	f.open(51, "fer", at, requests("robo"))
	f.open(52, "ana", at, requests("fer"))
	f.open(53, "ana", at, func(pr *dinv.PR) { pr.State = "MERGED" })
	f.st.SetOverride(context.Background(), dinv.Override{PR: ref(54), MutedUntil: fixtureNow.Add(time.Hour)})
	f.open(54, "fer", at, requests("bob"))
	got := map[int]api.ActionState{}
	for _, r := range f.rows("?view=all") {
		got[rowNumber(r)], _ = offerOf(r, "nudge")
	}
	want := map[int]api.ActionState{
		50: {ID: "nudge", Enabled: true, Hint: "Nudge @ana"},
		51: {ID: "nudge", Enabled: true, Hint: "Nudge " + prrow.NudgeMaintainers},
		52: {ID: "nudge", Reason: "acme/api#52 waits on you (review requested"},
		53: {},
		54: {ID: "nudge", Enabled: true, Hint: "Nudge @bob"},
	}
	for n, w := range want {
		g := got[n]
		if g.ID != w.ID || g.Enabled != w.Enabled || g.Hint != w.Hint || !strings.HasPrefix(g.Reason, w.Reason) {
			t.Errorf("#%d nudge %+v, want %+v", n, g, w)
		}
	}
}

func TestTheRowShowsTheQuestionAnAnswerReaches(t *testing.T) {
	f := newFixture(t)
	repo := f.withTurns()
	f.open(33, "ana", fixtureNow.Add(-time.Hour), requests("fer"))
	at := fixtureNow.Add(-30 * time.Minute)
	ref33 := pr.Ref(ref(33))
	b := turn.Book{Unit: ref33.Unit(), Turns: []turn.Turn{
		{Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.Automated, Session: "term", State: turn.Asked, SentAt: at, StartedAt: at},
		{Intent: turn.Intent(dintent.Probe), ID: "p1", Trigger: turn.ByHand, Session: "probe", State: turn.Asked, SentAt: at.Add(time.Minute), StartedAt: at.Add(time.Minute)},
	}}
	if _, err := repo.Save(context.Background(), b, port.Event{Unit: ref33.Unit(), Kind: turns.KindTurnSent, Key: "r", At: at}); err != nil {
		t.Fatal(err)
	}
	books, _ := repo.Books(context.Background(), ref33.Unit())
	b = books[ref33.Unit()]
	b.Record(turn.Record{TurnSeq: b.Turns[0].Seq, Session: "term", Hash: "a", At: at, Record: draftQuestion("publish?")})
	b.Record(turn.Record{TurnSeq: b.Turns[1].Seq, Session: "probe", Hash: "b", At: at.Add(2 * time.Minute),
		Record: record.Record{State: record.Asked, Summary: "which file?", Question: &record.Question{Kind: "free", Text: "Which file?"}}})
	if _, err := repo.Save(context.Background(), b, port.Event{Unit: ref33.Unit(), Kind: turns.KindTurnEnded, Key: "x", At: at}); err != nil {
		t.Fatal(err)
	}
	books, _ = repo.Books(context.Background(), ref33.Unit())
	b = books[ref33.Unit()]
	want, _ := b.Question([]string{"review", "address", "probe"})

	rows := f.rows("?view=" + api.ViewNeedsMe)
	if len(rows) != 1 || rows[0].Question == nil || rows[0].Question.Text != "Which file?" {
		t.Fatalf("the row shows another question than an answer reaches: %+v", rows)
	}
	var d api.Detail
	if code := get(t, f.s, "/api/units/github/acme%2Fapi%2333", &d); code != 200 || d.Turn == nil || d.Turn.Turn.Seq != want.Seq {
		t.Fatalf("%d %+v, want turn %d", code, d.Turn, want.Seq)
	}
}
