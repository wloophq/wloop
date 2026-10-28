package slack

import (
	"context"
	"errors"
	"github.com/wloophq/wloop/plugins/slack/store"
	"net/url"
	"slices"
	"strings"
	"testing"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/socket"
)

// watchedThread seeds a thread of chan1 by the viewer with one reply, so
// chan1 is admitted with its high-water mark at ts(2).
func watchedThread(t *testing.T, f *fixture, w *world) {
	t.Helper()
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(2), ReplyCount: 1,
		Messages: []dslack.Message{{TS: ts(1), User: me, Text: "parent"}, {TS: ts(2), ThreadTS: ts(1), User: other, Text: "first"}}}}})
	w.post(chan1, me, ts(1), "parent")
	w.post(chan1, other, ts(2), "first", ts(1))
}

func storedTS(t *testing.T, f *fixture, conv string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range f.stored(t, conv) {
		out[m.TS] = m.Via
	}
	return out
}

func TestCatchUpAdmitsLikeTheSocket(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	watchedThread(t, f, w)
	w.post(chan1, other, ts(5), "<@"+me+"> look")
	w.post(chan1, other, ts(6), "second", ts(1))
	w.post(chan1, other, ts(7), "firehose")
	r := &reasons{}
	f.p.poll.Search = r.search
	if err := f.p.poll.catchUp(context.Background(), ReasonReconnect); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	got := storedTS(t, f, chan1)
	if got[ts(5)] != string(dslack.ViaCatchUp) || got[ts(6)] != string(dslack.ViaCatchUp) {
		t.Fatalf("stored %v", got)
	}
	if _, ok := got[ts(7)]; ok {
		t.Fatal("a firehose message was stored")
	}
	if hwm := f.hwm(t, chan1); hwm != ts(7) {
		t.Fatalf("hwm %s", hwm)
	}
	if o, _ := f.object(t, dslack.ThreadObject(chan1, ts(1))); o.Version != ts(6) {
		t.Fatalf("thread version %q", o.Version)
	}
	if got := r.list(); !slices.Equal(got, []string{ReasonReconnect}) {
		t.Fatalf("searches %v", got)
	}
	s := f.p.slack.Metrics.snapshot()
	if s.LastCatchUp == nil || s.LastCatchUp.Reason != ReasonReconnect || s.LastCatchUp.Deltas != 2 || s.DeltasByVia["catchup"] != 2 {
		t.Fatalf("metrics %+v %+v", s.LastCatchUp, s.DeltasByVia)
	}
}

func TestCatchUpAndSocketCollapse(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	watchedThread(t, f, w)
	w.post(chan1, other, ts(5), "<@"+me+"> look")
	w.post(chan1, other, ts(6), "<@"+me+"> again")
	f.push(t, event("Ev1", evMsg(chan1, "channel", other, ts(5), "<@"+me+"> look")))
	if err := f.p.poll.catchUp(context.Background(), ReasonReconnect); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	f.push(t, event("Ev2", evMsg(chan1, "channel", other, ts(6), "<@"+me+"> again")))
	got := storedTS(t, f, chan1)
	if len(got) != 4 || got[ts(5)] != string(dslack.ViaSocket) || got[ts(6)] != string(dslack.ViaCatchUp) {
		t.Fatalf("stored %v", got)
	}
	var news int
	for _, c := range changesOf(t, f) {
		if c.TS == ts(5) || c.TS == ts(6) {
			news++
		}
	}
	if news != 2 {
		t.Fatalf("%d change rows for two messages", news)
	}
	if s := f.p.slack.Metrics.snapshot(); s.LastCatchUp.Deltas != 1 {
		t.Fatalf("a socket delta counted as caught up: %+v", s.LastCatchUp)
	}
}

func TestCatchUpPages(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	w.page = 2
	f.seed(t, dslack.Facts{DMs: []dslack.DMWindow{{Conversation: dm1, Messages: []dslack.Message{{TS: ts(1), User: other, Text: "hi"}}}}})
	w.post(dm1, other, ts(1), "hi")
	for i := 2; i <= 6; i++ {
		w.post(dm1, other, ts(i), "more")
	}
	if err := f.p.poll.catchUp(context.Background(), ReasonReconnect); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	if n := len(f.stored(t, dm1)); n != 6 {
		t.Fatalf("stored %d", n)
	}
	calls := f.api.Calls("conversations.history")
	if len(calls) != 3 || calls[0].Get("oldest") != ts(1) || calls[1].Get("cursor") != "2" || calls[2].Get("cursor") != "4" {
		t.Fatalf("history %v", calls)
	}
}

func TestCatchUpCountsMissedWhileConnected(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	watchedThread(t, f, w)
	f.p.onState(socket.State{Kind: socket.StateHello, At: tsTime(ts(0))})
	f.p.onState(socket.State{Kind: socket.StateDropped, At: tsTime(ts(200))})
	f.p.onState(socket.State{Kind: socket.StateHello, At: tsTime(ts(300))})
	w.post(chan1, other, ts(10), "<@"+me+"> while connected")
	w.post(chan1, other, ts(190), "<@"+me+"> just before the drop")
	w.post(chan1, other, ts(250), "<@"+me+"> while down")
	if err := f.p.poll.catchUp(context.Background(), ReasonReconnect); err != nil {
		t.Fatal(err)
	}
	s := f.p.slack.Metrics.snapshot()
	if s.LastCatchUp.Deltas != 3 || s.MissedWhileConnected != 1 {
		t.Fatalf("deltas %d, missed %d", s.LastCatchUp.Deltas, s.MissedWhileConnected)
	}
	f.flush(t)
	w.post(chan1, other, ts(20), "<@"+me+"> overflowed")
	if err := f.p.poll.catchUp(context.Background(), ReasonOverflow); err != nil {
		t.Fatal(err)
	}
	if s := f.p.slack.Metrics.snapshot(); s.MissedWhileConnected != 1 {
		t.Fatalf("an overflow counted as missed: %d", s.MissedWhileConnected)
	}
}

func TestCatchUpRecoversWhatTheInboxDropped(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	watchedThread(t, f, w)
	ctx := context.Background()
	w.post(chan1, other, ts(5), "<@"+me+"> look")
	w.post(chan1, other, ts(9), "firehose")
	f.p.deliver(ctx, delta(t, event("Ev1", evMsg(chan1, "channel", other, ts(5), "<@"+me+"> look"))))
	f.p.deliver(ctx, delta(t, event("Ev2", evMsg(chan1, "channel", other, ts(9), "firehose"))))
	if hwm := f.hwm(t, chan1); hwm != ts(9) {
		t.Fatalf("hwm %s", hwm)
	}
	obj := dslack.ThreadObject(chan1, ts(5))
	failed := errors.New("store down")
	for range touchAttempts {
		f.p.poll.touched([]string{obj}, failed)
		f.p.src.inbox.settle([]string{obj}, map[string]uint64{obj: 1}, failed)
	}
	if objs := f.pendingObjects(); len(objs) != 0 {
		t.Fatalf("the inbox kept %v", objs)
	}
	f.p.poll.due()
	if r := f.p.poll.gaps.take(); r != ReasonDropped {
		t.Fatalf("pending catch-up %q", r)
	}
	f.p.poll.runOnce(ctx, ReasonDropped)
	calls := f.api.Calls("conversations.history")
	if len(calls) != 1 || dslack.CompareTS(calls[0].Get("oldest"), ts(5)) >= 0 {
		t.Fatalf("history %v misses %s", calls, ts(5))
	}
	f.flush(t)
	if via := storedTS(t, f, chan1)[ts(5)]; via != string(dslack.ViaCatchUp) {
		t.Fatalf("the dropped mention: via %q", via)
	}
	f.p.poll.due()
	if r := f.p.poll.gaps.take(); r != "" || len(f.p.poll.floors) != 0 {
		t.Fatalf("the floor outlived its catch-up: %q %v", r, f.p.poll.floors)
	}
}

func TestViaIsOnlyCounted(t *testing.T) {
	type outcome struct {
		verdict verdict
		rows    []store.MessageRow
	}
	run := func(via dslack.Via) outcome {
		f := newFixture(t)
		f.login(t)
		f.knownUser(t, other, false)
		f.api.Handle("conversations.replies", func(url.Values) any {
			return map[string]any{"messages": []any{
				map[string]any{"type": "message", "user": me, "ts": ts(1), "text": "parent", "thread_ts": ts(1), "reply_count": 1, "latest_reply": ts(3)},
				map[string]any{"type": "message", "user": other, "ts": ts(3), "text": "<@" + me + "> look", "thread_ts": ts(1)},
			}}
		})
		d := delta(t, event("Ev1", evMsg(chan1, "channel", other, ts(3), "<@"+me+"> look", "thread_ts", ts(1))))
		d.Via = via
		v, err := f.p.src.admit.admit(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		f.p.deliver(context.Background(), d)
		f.flush(t)
		rows := f.stored(t, chan1)
		for i := range rows {
			if rows[i].TS == ts(3) && rows[i].Via != string(via) {
				t.Fatalf("via %q stored as %q", via, rows[i].Via)
			}
			rows[i].Via, rows[i].ReceivedAt = "", rows[i].ReceivedAt.UTC()
		}
		return outcome{v, rows}
	}
	want := run(dslack.ViaSocket)
	if want.verdict.rule != RuleMention || len(want.rows) != 2 {
		t.Fatalf("socket outcome %+v", want)
	}
	for _, via := range vias[1:] {
		got := run(via)
		if got.verdict != want.verdict || len(got.rows) != len(want.rows) {
			t.Fatalf("via %s: %+v, socket %+v", via, got, want)
		}
		for i := range got.rows {
			a, b := got.rows[i], want.rows[i]
			if a.TS != b.TS || a.Text != b.Text || a.User != b.User || a.ThreadTS != b.ThreadTS {
				t.Fatalf("via %s stored %+v, socket %+v", via, a, b)
			}
		}
	}
}

func TestFollowInLegacyGroupDMWatchesTheConversation(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "is_group": true, "is_mpim": true, "is_private": true}}
	})
	ctx := context.Background()
	err := f.p.track(ctx, dslack.ThreadUnit(team, "G0001", ts(1)), true)
	if err != nil {
		t.Fatal(err)
	}
	if o, ok := f.object(t, dslack.DMObject("G0001")); !ok || !o.Watched {
		t.Fatalf("group DM object %+v", o)
	}
	if _, ok := f.object(t, dslack.ThreadObject("G0001", ts(1))); ok {
		t.Fatal("the thread object of a group DM was watched")
	}
	err = f.p.track(ctx, dslack.ThreadUnit(team, "G0001", ts(2)), true)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.api.Calls("conversations.info")); n != 1 {
		t.Fatalf("conversations.info %d calls", n)
	}
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "is_group": true, "is_private": true}}
	})
	err = f.p.track(ctx, dslack.ThreadUnit(team, "G0002", ts(1)), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.object(t, dslack.ThreadObject("G0002", ts(1))); !ok {
		t.Fatal("a private channel thread was not watched as a thread")
	}
}

func TestCatchUpStoresEditsMissedWhileDown(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	f.seed(t, dslack.Facts{DMs: []dslack.DMWindow{{Conversation: dm1, Messages: []dslack.Message{
		{TS: ts(1), User: other, Text: "hi"}, {TS: ts(2), User: other, Text: "typo"}}}}})
	w.post(dm1, other, ts(1), "hi")
	w.post(dm1, other, ts(2), "fixed")
	w.mu.Lock()
	w.msgs[dm1][1]["edited"] = map[string]any{"ts": ts(3)}
	w.mu.Unlock()
	if err := f.db.Slack().MarkSeen(context.Background(), dm1, ts(1)); err != nil {
		t.Fatal(err)
	}
	if err := f.p.poll.catchUp(context.Background(), ReasonStart); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	ms := f.stored(t, dm1)
	if len(ms) != 2 || ms[1].Text != "fixed" || ms[1].EditedTS != ts(3) {
		t.Fatalf("stored %+v", ms)
	}
}

func TestCatchUpReadsDMsOfConversationsWithNothingAdmitted(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	watchedThread(t, f, w)
	w.post("D0009", other, ts(5), "first DM ever")
	w.post("D0009", other, ts(6), "are you there?")
	if err := f.p.poll.catchUp(context.Background(), ReasonClockJump); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	if got := storedTS(t, f, "D0009"); len(got) != 2 || got[ts(5)] != string(dslack.ViaCatchUp) {
		t.Fatalf("stored %v", got)
	}
	qs := w.searched()
	if len(qs) != 2 || !strings.HasPrefix(qs[0], DMSearch+" after:") || !strings.HasPrefix(qs[1], "<@"+me+"> after:") {
		t.Fatalf("searches %v", qs)
	}
}

func TestCatchUpReadsRepliesUnderOldDMParents(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	f.seed(t, dslack.Facts{DMs: []dslack.DMWindow{{Conversation: dm1, Messages: []dslack.Message{
		{TS: ts(1), User: other, Text: "a question"}, {TS: ts(2), ThreadTS: ts(1), User: me, Text: "an answer"},
		{TS: ts(3), User: other, Text: "a lone message"}, {TS: ts(9), User: other, Text: "later"}}}}})
	w.post(dm1, other, ts(1), "a question")
	w.post(dm1, me, ts(2), "an answer", ts(1))
	w.post(dm1, other, ts(3), "a lone message")
	w.post(dm1, other, ts(9), "later")
	w.post(dm1, other, ts(20), "a follow-up", ts(1))
	if err := f.p.poll.catchUp(context.Background(), ReasonClockJump); err != nil {
		t.Fatal(err)
	}
	if calls := f.api.Calls("conversations.replies"); len(calls) != 2 || calls[0].Get("limit") != "1" {
		t.Fatalf("a DM without threads was probed: %v", calls)
	}
	f.flush(t)
	got := storedTS(t, f, dm1)
	if got[ts(20)] != string(dslack.ViaCatchUp) {
		t.Fatalf("stored %v", got)
	}
}

func TestCatchUpReadsAFirstReplyUnderAnOldLoneDM(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	f.seed(t, dslack.Facts{DMs: []dslack.DMWindow{{Conversation: dm1, Messages: []dslack.Message{
		{TS: ts(3), User: me, Text: "a lone message"}, {TS: ts(900), User: other, Text: "later"}}}}})
	w.post(dm1, me, ts(3), "a lone message")
	w.post(dm1, other, ts(900), "later")
	w.post(dm1, other, ts(950), "a first reply", ts(3))
	if err := f.p.poll.catchUp(context.Background(), ReasonClockJump); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
	if got := storedTS(t, f, dm1); got[ts(950)] != string(dslack.ViaCatchUp) {
		t.Fatalf("stored %v", got)
	}
}

func TestDMSearchThatIgnoresTheModifierIsLoggedOnce(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	w.ignoreDMSearch = true
	watchedThread(t, f, w)
	w.post(chan1, other, ts(5), "firehose")
	for range 2 {
		if err := f.p.poll.catchUp(context.Background(), ReasonClockJump); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(f.logs.String(), "ignored "+DMSearch); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, f.logs)
	}
}

func TestFailedDMSearchKeepsTheCatchUp(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	watchedThread(t, f, w)
	w.post(chan1, other, ts(3), "missed reply", ts(1))
	f.api.Handle("search.messages", func(url.Values) any { return map[string]any{"ok": false, "error": "missing_scope"} })
	ctx := context.Background()
	for range 2 {
		if err := f.p.poll.catchUp(ctx, ReasonClockJump); err != nil {
			t.Fatalf("catch-up: %v", err)
		}
	}
	f.flush(t)
	if got := storedTS(t, f, chan1); got[ts(3)] != string(dslack.ViaCatchUp) {
		t.Fatalf("the rest of the catch-up did not run: stored %v", got)
	}
	if s := f.p.slack.Metrics.snapshot(); s.LastCatchUp == nil || s.LastCatchUp.Reason != ReasonClockJump {
		t.Fatalf("last catch-up %+v", s.LastCatchUp)
	}
	if n := strings.Count(f.logs.String(), "the DM search fails"); n != 1 {
		t.Fatalf("DM search failure logged %d times:\n%s", n, f.logs)
	}
	if h, _ := f.health(t); !warned(h, "DM search of the catch-up fails") {
		t.Fatalf("warnings %v", h.Warnings)
	}
}
