package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/link"
	"github.com/wloophq/wloop/plugins/slack/socket"
	"github.com/wloophq/wloop/plugins/slack/socket/sockettest"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

func TestPluginServesEveryCapability(t *testing.T) {
	want := []src.Capability{src.CapabilityIngester, src.CapabilityPresenter, src.CapabilityPublisher, src.CapabilityLander,
		src.CapabilityBriefer, src.CapabilityPlanner, src.CapabilityTooler, src.CapabilityCLIRunner}
	if got := src.Capabilities(&Plugin{}); !slices.Equal(got, want) {
		t.Fatalf("capabilities %v, want %v", got, want)
	}
	f := newFixture(t)
	bare := newPlugin(f.cfg, host.Broker{}, nil, Slack{}, nil)
	summary, skip, err := bare.ingestJob(context.Background())
	if err != nil || summary != "skipped" || skip != SkipNotConfigured {
		t.Fatalf("without a store: %q %q %v", summary, skip, err)
	}
	if err := runFor(bare, 50*time.Millisecond); err != nil {
		t.Fatalf("Run without a store: %v", err)
	}
}

func TestModuleIdleWithoutLogin(t *testing.T) {
	for _, c := range []struct {
		name      string
		user, app bool
	}{{"no user token", false, true}, {"no app token", true, false}, {"neither", false, false}} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			srv := sockettest.New(t)
			f.slack.Open = srv.Open
			f.slack.Recheck = 10 * time.Millisecond
			f.build(t)
			svc := f.cfg.Service()
			if c.user {
				raw, _ := json.Marshal(auth.Token{Team: team, User: me, Access: fakeUserToken})
				f.kc.Set(svc, "current", raw)
			}
			if c.app {
				f.kc.Set(svc, "app", []byte(fakeAppToken))
			}
			summary, skipped, err := f.p.ingestJob(context.Background())
			if err != nil || summary != "skipped" || skipped != SkipNotLoggedIn {
				t.Fatalf("job: %q %q %v", summary, skipped, err)
			}
			if err := runFor(f.p, 100*time.Millisecond); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if srv.Opens() != 0 {
				t.Fatal("Run dialed without a login")
			}
			if f.kc.has("current") != c.user || f.kc.has("app") != c.app {
				t.Fatal("a token was deleted")
			}
		})
	}
}

func TestRunStreamsIntoTheStore(t *testing.T) {
	f := newFixture(t)
	f.namedChannel(t, chan1)
	f.knownUser(t, other, false)
	srv := sockettest.New(t)
	stop := f.running(t, srv)
	srv.Hello(1)
	srv.Send(envelope("env-1", 0, event("Ev1", evMsg(dm1, "im", other, ts(1), "hello"))))
	srv.Send(envelope("env-2", 0, event("Ev2", evMsg(chan1, "channel", other, ts(2), "firehose"))))
	srv.Send(envelope("env-3", 0, event("Ev3", evMsg(chan1, "channel", other, ts(3), "<@"+me+"> look"))))
	waitFor(t, "two stored messages", func() bool { return len(f.stored(t, dm1)) == 1 && len(f.stored(t, chan1)) == 1 })
	for _, m := range append(f.stored(t, dm1), f.stored(t, chan1)...) {
		if m.Via != string(dslack.ViaSocket) {
			t.Fatalf("message %s via %q", m.TS, m.Via)
		}
	}
	s := f.p.slack.Metrics.snapshot()
	if s.EventsPerHour.Received != 3 || s.EventsPerHour.Admitted != 2 || s.DeltasByVia["socket"] != 2 || s.Socket.State != "connected" {
		t.Fatalf("metrics %+v", s)
	}
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	if calls := f.calls(apiMethods...); len(calls) != 0 {
		t.Fatalf("Slack calls %v", calls)
	}
}

func TestTrackWatchesTheObject(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	ctx := context.Background()
	plug := &Plugin{p: f.p}
	for u, object := range map[unit.Unit]string{
		dslack.ThreadUnit(team, chan1, ts(1)):                               dslack.ThreadObject(chan1, ts(1)),
		dslack.ThreadUnit(team, dm1, ts(2)):                                 dslack.DMObject(dm1),
		dslack.Key{Team: team, Conversation: "D0002", Segment: "s1"}.Unit(): dslack.DMObject("D0002"),
	} {
		read, err := plug.Track(ctx, src.TrackRequest{Unit: u.Wire()})
		if err != nil || read.Unit != u.Wire() {
			t.Fatalf("track %s: %+v %v", u, read, err)
		}
		if _, ok := f.object(t, object); ok {
			t.Fatalf("%s watched by a read the host did not follow", object)
		}
		if _, err := plug.Track(ctx, src.TrackRequest{Unit: u.Wire(), Follow: true}); err != nil {
			t.Fatal(err)
		}
		o, ok := f.object(t, object)
		if !ok || !o.Watched || !slices.Contains(o.Reasons, string(dslack.ReasonUserFollow)) {
			t.Fatalf("%s: %+v", object, o)
		}
	}
	if _, err := plug.Track(ctx, src.TrackRequest{Unit: unit.Unit{Provider: unit.ProviderID("github"), Key: "acme/api#1"}.Wire(), Follow: true}); !errors.Is(err, src.ErrNotFound) {
		t.Fatalf("tracked a github unit: %v", err)
	}
}

func TestFollowedThreadIsReadOnTheNextPass(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.api.Handle("conversations.replies", func(url.Values) any {
		return map[string]any{"messages": []any{
			map[string]any{"type": "message", "user": other, "ts": ts(1), "text": "parent", "thread_ts": ts(1), "reply_count": 1, "latest_reply": ts(2)},
			map[string]any{"type": "message", "user": other, "ts": ts(2), "text": "reply", "thread_ts": ts(1)},
		}}
	})
	ctx := context.Background()
	err := f.p.track(ctx, dslack.ThreadUnit(team, chan1, ts(1)), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	if ms := f.stored(t, chan1); len(ms) != 2 {
		t.Fatalf("stored %d", len(ms))
	}
	if o, _ := f.object(t, dslack.ThreadObject(chan1, ts(1))); o.Version != ts(2) {
		t.Fatalf("version %q", o.Version)
	}
	f.now = f.now.Add(PollCold + time.Minute)
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := f.api.Calls("conversations.replies"); len(calls) != 2 || calls[1].Get("limit") != "1" {
		t.Fatalf("an unmoved thread was read again: %v", calls)
	}
}

func TestSnapshotCarriesTheLinksOfItsMessages(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	if err := f.p.registration().Valid("acme/api#7"); err == nil {
		t.Fatal("the registration takes a key that is not Slack's")
	}
	f.knownUser(t, other, false)
	f.push(t, event("Ev1", evMsg(chan1, "channel", other, ts(1), "<@"+me+"> review <https://github.com/acme/api/pull/7|#7>")))
	f.push(t, event("Ev2", evMsg(chan1, "channel", other, ts(3), "thanks", "thread_ts", ts(1), "parent_user_id", other)))
	u := dslack.ThreadUnit(team, chan1, ts(1))
	f.report(t)
	ls := f.reported(t, u).Links
	if len(ls) != 1 || ls[0].URL != "https://github.com/acme/api/pull/7" || ls[0].Kind != string(link.Mention) {
		t.Fatalf("links %+v", ls)
	}
	if !ls[0].Evidence.At.Equal(dslack.TSTime(ts(1))) || ls[0].Evidence.Ref != chan1+"/"+ts(1) || ls[0].Evidence.Unit != u.Wire() {
		t.Fatalf("link evidence %+v", ls[0].Evidence)
	}
}

func TestSocketDeltaCostsNoAPICall(t *testing.T) {
	f := newFixture(t)
	f.namedChannel(t, chan1)
	f.knownUser(t, other, false)
	srv := sockettest.New(t)
	f.running(t, srv)
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(2), ReplyCount: 1, Messages: []dslack.Message{
		{TS: ts(1), User: me, Text: "parent"}, {TS: ts(2), ThreadTS: ts(1), User: other, Text: "first"}}}}})
	srv.Hello(1)
	srv.Send(envelope("env-1", 0, event("Ev1", evMsg(chan1, "channel", other, ts(3), "second", "thread_ts", ts(1), "parent_user_id", me))))
	waitFor(t, "the reply stored", func() bool { return len(f.stored(t, chan1)) == 3 })
	if calls := f.calls(apiMethods...); len(calls) != 0 {
		t.Fatalf("Slack calls %v", calls)
	}
	o, _ := f.object(t, dslack.ThreadObject(chan1, ts(1)))
	if o.Version != ts(3) || o.ReplyCount != 2 {
		t.Fatalf("object %+v", o)
	}
}

func TestUnknownThreadReadsParent(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.api.Handle("conversations.replies", func(v url.Values) any {
		if v.Get("ts") != ts(1) {
			return map[string]any{"ok": false, "error": "thread_not_found"}
		}
		return map[string]any{"messages": []any{
			map[string]any{"type": "message", "user": other, "ts": ts(1), "text": "parent", "thread_ts": ts(1), "reply_count": 2, "latest_reply": ts(3)},
			map[string]any{"type": "message", "user": other, "ts": ts(2), "text": "earlier", "thread_ts": ts(1),
				"reactions": []any{map[string]any{"name": "eyes", "users": []string{me}, "count": 1}}},
			map[string]any{"type": "message", "subtype": "bot_message", "bot_id": "B0001", "ts": "1700000002.000200", "text": "bot", "thread_ts": ts(1)},
			map[string]any{"type": "message", "user": other, "ts": ts(3), "text": "hey <@" + me + ">", "thread_ts": ts(1)},
		}}
	})
	f.push(t, event("Ev1", evMsg(chan1, "channel", other, ts(3), "hey <@"+me+">", "thread_ts", ts(1))))
	if n := len(f.api.Calls("conversations.replies")); n != 1 {
		t.Fatalf("replies calls %d", n)
	}
	ms := f.stored(t, chan1)
	var got []string
	for _, m := range ms {
		got = append(got, m.TS)
	}
	if !slices.Equal(got, []string{ts(1), ts(2), ts(3)}) {
		t.Fatalf("stored %v", got)
	}
	if len(ms[1].Reactions) != 1 {
		t.Fatalf("reactions %+v", ms[1].Reactions)
	}
}

func reaction(id, kind, user, name, ts string) json.RawMessage {
	return event(id, map[string]any{"type": kind, "user": user, "reaction": name,
		"item": map[string]any{"type": "message", "channel": chan1, "ts": ts}})
}

func TestRemovingTheLastReactionIsStored(t *testing.T) {
	f := newFixture(t)
	f.namedChannel(t, chan1)
	f.login(t)
	f.knownUser(t, other, false)
	f.push(t, event("Ev1", evMsg(chan1, "channel", other, ts(1), "<@"+me+"> ship it")))
	f.push(t, reaction("Ev2", "reaction_added", me, "eyes", ts(1)), reaction("Ev3", "reaction_added", other, "eyes", ts(1)))
	if ms := f.stored(t, chan1); len(ms[0].Reactions) != 1 || len(ms[0].Reactions[0].Users) != 2 {
		t.Fatalf("after adding: %+v", ms[0].Reactions)
	}
	f.push(t, reaction("Ev4", "reaction_removed", me, "eyes", ts(1)))
	f.push(t, reaction("Ev5", "reaction_removed", other, "eyes", ts(1)))
	if ms := f.stored(t, chan1); len(ms[0].Reactions) != 0 {
		t.Fatalf("after removing the last: %+v", ms[0].Reactions)
	}
	var kinds []dslack.ChangeKind
	for _, c := range changesOf(t, f) {
		kinds = append(kinds, c.Kind)
	}
	if want := []dslack.ChangeKind{dslack.ChangeNew, dslack.ChangeReaction, dslack.ChangeReaction, dslack.ChangeReaction}; !slices.Equal(kinds, want) {
		t.Fatalf("changes %v", kinds)
	}
	if calls := f.calls(apiMethods...); len(calls) != 0 {
		t.Fatalf("Slack calls %v", calls)
	}
	f.push(t, reaction("Ev6", "reaction_added", me, "eyes", "1700009999.000100"))
	if n := len(changesOf(t, f)); n != 4 {
		t.Fatalf("a reaction on an unknown message changed the store: %d changes", n)
	}
}

func TestDeleteOfAReplyStaysInItsThread(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(2), ReplyCount: 1, Messages: []dslack.Message{
		{TS: ts(1), User: me, Text: "parent"}, {TS: ts(2), ThreadTS: ts(1), User: other, Text: "reply"}}}}})
	f.push(t, event("Ev1", map[string]any{"type": "message", "subtype": "message_deleted", "channel": chan1, "channel_type": "channel",
		"ts": ts(4), "deleted_ts": ts(2)}))
	ms := f.stored(t, chan1)
	if len(ms) != 2 || !ms[1].Deleted || ms[1].Text != "" {
		t.Fatalf("stored %+v", ms)
	}
}

var tokenPattern = regexp.MustCompile(`x(ox[pbaer]|app)-|ticket=`)

func TestSourceErrorsHoldNoToken(t *testing.T) {
	f := newFixture(t)
	f.slack.Open = func(context.Context) (string, error) {
		return "ws://127.0.0.1:1/link/?ticket=fake-ticket-1&app_id=A000", nil
	}
	f.build(t)
	f.login(t)
	var errs []string
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	err := f.p.run(ctx)
	cancel()
	errs = append(errs, err.Error())

	f.slack.Open = nil
	f.build(t)
	f.login(t)
	ctx, cancel = context.WithTimeout(context.Background(), 1500*time.Millisecond)
	f.api.Handle("apps.connections.open", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_auth"} })
	if err := f.p.run(ctx); err == nil || !errors.Is(err, socket.ErrInvalidAuth) {
		t.Fatalf("Run with a refused app token: %v", err)
	} else {
		errs = append(errs, err.Error())
	}
	cancel()

	f.api.Close()
	f.p.deliver(context.Background(), delta(t, event("Ev1", evMsg(dm1, "im", other, ts(1), "hello"))))
	f.p.deliver(context.Background(), delta(t, event("Ev2", evMsg(chan1, "channel", other, ts(3), "<@"+me+">", "thread_ts", ts(1)))))
	if err := f.p.touch(context.Background(), []string{dslack.ThreadObject(chan1, ts(1))}); err == nil {
		t.Fatal("a failed conversations.replies did not fail the pass")
	} else {
		errs = append(errs, err.Error())
	}
	all := strings.Join(errs, "\n") + "\n" + f.logs.String()
	if !strings.Contains(f.logs.String(), "users.info") {
		t.Fatalf("no users.info failure logged: %s", f.logs)
	}
	if tokenPattern.MatchString(all) || strings.Contains(all, "fakeuser") || strings.Contains(all, "fakeapp") {
		t.Fatalf("token material leaked:\n%s", all)
	}
}

func TestLastReactionRemovalIsStored(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	reactions := []any{map[string]any{"name": "eyes", "users": []string{me}, "count": 1}}
	f.api.Handle("conversations.replies", func(url.Values) any {
		parent := map[string]any{"type": "message", "user": other, "ts": ts(1), "text": "<@" + me + "> ship it"}
		if reactions != nil {
			parent["reactions"] = reactions
		}
		return map[string]any{"messages": []any{parent}}
	})
	ctx := context.Background()
	obj := dslack.ThreadObject(chan1, ts(1))
	if err := f.p.touch(ctx, []string{obj}); err != nil {
		t.Fatal(err)
	}
	if ms := f.stored(t, chan1); len(ms) != 1 || len(ms[0].Reactions) != 1 {
		t.Fatalf("first read: %+v", ms)
	}
	reactions = nil
	if err := f.p.touch(ctx, []string{obj}); err != nil {
		t.Fatal(err)
	}
	if ms := f.stored(t, chan1); len(ms[0].Reactions) != 0 {
		t.Fatalf("re-read: %+v", ms[0].Reactions)
	}
	var kinds []dslack.ChangeKind
	for _, c := range changesOf(t, f) {
		kinds = append(kinds, c.Kind)
	}
	if !slices.Equal(kinds, []dslack.ChangeKind{dslack.ChangeNew, dslack.ChangeReaction}) {
		t.Fatalf("changes %v", kinds)
	}
}

// runFor runs p's stream until d passes, and fails when Run outlives ctx.
func runFor(p *plugin, d time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.run(ctx) }()
	select {
	case err := <-done:
		if ctx.Err() == nil {
			return errors.New("Run returned before its context ended")
		}
		return err
	case <-time.After(d + 5*time.Second):
		return errors.New("Run outlived its context")
	}
}

func TestIdleRunStartsOnceLoggedIn(t *testing.T) {
	f := newFixture(t)
	srv := sockettest.New(t)
	f.slack.Open = srv.Open
	f.slack.Recheck = 10 * time.Millisecond
	f.build(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	looked := f.kc.readsOf("current")
	go func() { done <- f.p.run(ctx) }()
	waitFor(t, "a second look for a login", func() bool { return f.kc.readsOf("current") >= looked+2 })
	if srv.Opens() != 0 {
		t.Fatal("dialed while logged out")
	}
	f.login(t)
	waitFor(t, "the socket opened after the login", func() bool { return srv.Opens() > 0 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	if strings.Contains(f.logs.String(), "not logged in") {
		t.Fatalf("the idle wait logged: %s", f.logs)
	}
}

func TestUnfollowUnwatchesTheObject(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	ctx := context.Background()
	u := dslack.ThreadUnit(team, chan1, ts(1))
	if err := f.p.track(ctx, u, true); err != nil {
		t.Fatal(err)
	}
	reply := func(id, at string) dslack.Delta {
		return delta(t, event(id, evMsg(chan1, "channel", other, at, "news", "thread_ts", ts(1), "parent_user_id", other)))
	}
	if v, err := f.p.src.admit.admit(ctx, reply("Ev1", ts(2))); err != nil || v.rule != RuleFollowed {
		t.Fatalf("followed: %+v %v", v, err)
	}
	if err := f.p.untrack(ctx, u); err != nil {
		t.Fatal(err)
	}
	if o, ok := f.object(t, dslack.ThreadObject(chan1, ts(1))); !ok || o.Watched {
		t.Fatalf("after unfollow: %+v", o)
	}
	if v, err := f.p.src.admit.admit(ctx, reply("Ev2", ts(3))); err != nil || v.rule != "" {
		t.Fatalf("a reply after unfollow: %+v %v", v, err)
	}
}
