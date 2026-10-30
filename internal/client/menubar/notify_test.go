package menubar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const stamp = "20060102T150405.000000000"

func taskID(n int) string { return fmt.Sprintf("e-%d", n) }

func pendingTask(n int) api.TaskView {
	e := plainTask(n)
	e.Title = "Round soon"
	e.Attention = &api.Attention{ID: api.NotificationID(api.CategoryInbox, e.ID, now.UTC().Format(stamp)), Category: "INBOX", Title: "Round in 60 s",
		Subtitle: uref(n).Key, Body: "follow-up: ana replied · waiting 20 min", Image: "round",
		Actions: []api.AttentionAction{{ID: "task:approve", Title: "Run now"}, {ID: "task:dismiss", Title: "Skip"}, {ID: "guide", Title: "Guide", TextInput: true, Basis: "b-guide"},
			{ID: "hold", Title: "Hold", Basis: "b-hold"}, {ID: "open", Title: "Open"}}}
	return e
}

func heldTask(n int, day time.Time) api.TaskView {
	e := plainTask(n)
	e.Attention = &api.Attention{ID: api.NotificationID(api.CategoryHeld, e.ID, day.Local().Format(time.DateOnly)), Category: "HELD", Title: "Round held",
		Subtitle: uref(n).Key, Body: "automatic round held: daily cap", Image: "held",
		Actions: []api.AttentionAction{{ID: "task:approve", Title: "Run now"}, {ID: "hold", Title: "Hold"}, {ID: "open", Title: "Open"}}}
	return e
}

func waitTask(n int, token string) api.TaskView {
	e := plainTask(n)
	e.Attention = &api.Attention{ID: api.NotificationID(api.CategoryInbox, e.ID, token), Category: "INBOX", Title: "Waiting for a permission",
		Subtitle: uref(n).Key, Body: "the agent may be waiting for you", Image: "ask",
		Actions: []api.AttentionAction{{ID: "hold", Title: "Hold"}, {ID: "open", Title: "Open"}}}
	return e
}

func questionTask(n int) api.TaskView {
	e := plainTask(n)
	e.Title = "Publish the review?"
	subject, _ := json.Marshal(api.Message{ID: int64(n), Unit: uref(n), Kind: "question", Text: "Publish the review?", Recommend: "publish",
		Options: []record.Option{
			{ID: "comment", Label: "Comment only", Consequence: "posts a comment"},
			{ID: "publish", Label: "Publish APPROVE", Consequence: "approves", Publishes: true},
			{ID: "hold", Label: "Hold the draft", Consequence: "nothing is posted"},
		}})
	e.Gates = []api.GateView{{ID: int64(n), Kind: "question", Status: "open", Intents: []api.IntentView{}, Subject: subject}}
	e.Attention = &api.Attention{ID: api.NotificationID("Q5", e.ID, "5"), Category: "Q5", Title: "Agent asks", Subtitle: uref(n).Key, Image: "ask",
		Body: "the draft approves; publish it?",
		Actions: []api.AttentionAction{
			{ID: "answer:option:publish", Title: "Publish APPROVE (recommended)"}, {ID: "answer:option:comment", Title: "Comment only"},
			{ID: "answer:option:hold", Title: "Hold the draft"}, {ID: "answer:text", Title: "Reply", TextInput: true}, {ID: "task:dismiss", Title: "Dismiss"},
		}}
	return e
}

func draftTask(n int) api.TaskView {
	id := fmt.Sprintf("draft:%d", n)
	return api.TaskView{ID: id, State: "inbox", Title: "reply: on it", Units: []unit.Unit{uref(n)},
		Attention: &api.Attention{ID: api.NotificationID(api.CategoryOutbox, id, now.UTC().Format(stamp)), Category: "OUTBOX", Title: "Draft to approve",
			Subtitle: uref(n).Key, Body: "reply: on it", Actions: []api.AttentionAction{{ID: "open", Title: "Open"}, {ID: "task:dismiss", Title: "Discard"}}}}
}

func buttons(n Notification) []string {
	var out []string
	for _, a := range n.Actions {
		s := a.ID + " " + a.Title
		if a.TextInput {
			s += " [text]"
		}
		out = append(out, s)
	}
	return out
}

func served(a *api.Attention) []string {
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

func (f *fakeNotifier) snapshot() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.posted)
}

func (f *fakeNotifier) withdrawnIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.withdrawn)
}

func TestTheNotificationIsTheAttentionAsGiven(t *testing.T) {
	r := newRig(t)
	e := pendingTask(1)
	r.d.tasks["inbox"] = []api.TaskView{e, plainTask(2)}
	r.c.Refresh(context.Background())
	r.c.Refresh(context.Background())
	if len(r.notes.posted) != 1 {
		t.Fatalf("posted %+v", r.notes.posted)
	}
	a, n := e.Attention, r.notes.posted[0]
	if cat, _, _ := api.ParseNotificationID(n.ID); n.ID != a.ID || cat != api.CategoryInbox || !strings.HasPrefix(string(n.Category), "INBOX.") || n.Title != a.Title || n.Subtitle != a.Subtitle || n.Body != a.Body || n.Image != ImageRound ||
		!slices.Equal(buttons(n), served(a)) {
		t.Fatalf("%+v", n)
	}
}

func TestNotificationButtonsAreTheServedActions(t *testing.T) {
	for name, e := range map[string]api.TaskView{"pending": pendingTask(1), "held": heldTask(2, now), "wait": waitTask(3, "a"), "question": questionTask(4), "draft": draftTask(5)} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.d.tasks["inbox"] = []api.TaskView{e}
			r.c.Refresh(context.Background())
			if len(r.notes.posted) != 1 {
				t.Fatalf("posted %+v", r.notes.posted)
			}
			n := r.notes.posted[0]
			if !slices.Equal(buttons(n), served(e.Attention)) {
				t.Fatalf("buttons %q\nserved  %q", buttons(n), served(e.Attention))
			}
			if len(r.notes.categoryActions(n.Category)) == 0 {
				t.Errorf("category %s registered with no buttons", n.Category)
			}
		})
	}
}

func TestOneNotificationPerTaskInSeveralViews(t *testing.T) {
	r := newRig(t)
	e := pendingTask(1)
	r.d.tasks["focus"] = []api.TaskView{e}
	r.d.tasks["inbox"] = []api.TaskView{e}
	r.c.Refresh(context.Background())
	if len(r.notes.posted) != 1 {
		t.Fatalf("posted %+v", r.notes.posted)
	}
}

func TestLeavingInboxWithdraws(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	inbox := []api.TaskView{pendingTask(1), waitTask(5, "a"), heldTask(4, now), questionTask(6)}
	r.d.tasks["inbox"] = inbox
	r.d.tasks["outbox"] = []api.TaskView{draftTask(7)}
	r.c.Refresh(ctx)
	if len(r.notes.posted) != 5 || !slices.Contains(r.notes.categories(), "Q5") {
		t.Fatalf("posted %+v categories %q", r.notes.posted, r.notes.categories())
	}
	r.d.tasks["inbox"], r.d.tasks["outbox"] = nil, nil
	r.c.Refresh(ctx)
	var want []string
	for _, e := range append(slices.Clone(inbox), draftTask(7)) {
		want = append(want, e.Attention.ID)
	}
	slices.Sort(want)
	if got := slices.Sorted(slices.Values(r.notes.withdrawn)); !slices.Equal(got, want) {
		t.Fatalf("withdrawn %q\nwant      %q", got, want)
	}
	if slices.Contains(r.notes.categories(), "Q5") {
		t.Fatalf("the question's category outlived it: %q", r.notes.categories())
	}
	r.c.Refresh(ctx)
	if len(r.notes.withdrawn) != len(want) {
		t.Fatalf("withdrew again: %q", r.notes.withdrawn)
	}
	r.d.tasks["inbox"] = []api.TaskView{waitTask(5, "a"), heldTask(4, now)}
	r.c.Refresh(ctx)
	if len(r.notes.posted) != 6 || r.notes.posted[5].ID != waitTask(5, "a").Attention.ID {
		t.Fatalf("a task back in the Inbox was not posted, or a held one twice a day: %+v", r.notes.posted[5:])
	}
}

func TestANewAttentionIDReplacesTheNotification(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{waitTask(5, "a")}
	r.c.Refresh(context.Background())
	r.d.tasks["inbox"] = []api.TaskView{waitTask(5, "b")}
	r.c.Refresh(context.Background())
	if len(r.notes.posted) != 2 || r.notes.posted[1].ID == r.notes.posted[0].ID || !slices.Equal(r.notes.withdrawn, []string{r.notes.posted[0].ID}) {
		t.Fatalf("posted %+v withdrawn %v", r.notes.posted, r.notes.withdrawn)
	}
}

func TestATaskWithoutAttentionIsNotNotified(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{plainTask(1)}
	r.d.rows["needs-me"] = []api.Row{reviewRow(1)}
	r.c.Refresh(context.Background())
	if len(r.notes.posted) != 0 {
		t.Fatalf("posted %+v", r.notes.posted)
	}
}

func TestResponsesBecomeTaskVerbs(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(1), questionTask(6)}
	r.c.Refresh(ctx)
	id, q := pendingTask(1).Attention.ID, questionTask(6).Attention.ID
	for _, c := range []struct{ id, action, text string }{
		{id, "task:approve", ""}, {id, "task:dismiss", ""}, {id, "guide", "look at the retry"}, {id, "hold", ""}, {id, "open", ""},
		{q, "answer:option:publish", ""},
	} {
		resp, ok := ResponseFor(c.id, c.action)
		if !ok {
			t.Fatalf("%s is no response", c.action)
		}
		resp.Text = c.text
		r.c.Respond(ctx, resp)
	}
	var verbs []string
	for _, v := range r.d.snapshotVerbs() {
		verbs = append(verbs, fmt.Sprintf("%s %s %v %s", v.id, v.verb, v.req.Args, v.req.Source))
	}
	wantVerbs := []string{"e-1 approve map[] menubar", "e-1 dismiss map[] menubar",
		"e-6 answer map[guidance:Publish APPROVE (option publish) option_id:publish] notification"}
	if !slices.Equal(verbs, wantVerbs) {
		t.Fatalf("verbs %q\nwant  %q", verbs, wantVerbs)
	}
	wantActions := []string{"guide acme/api#1 look at the retry", "hold acme/api#1", "open acme/api#1"}
	if got := r.d.snapshotActions(); !slices.Equal(got, wantActions) {
		t.Fatalf("actions %q\nwant    %q", got, wantActions)
	}
	if len(r.openedURLs()) != 0 {
		t.Fatalf("a served open went to the cockpit: %v", r.openedURLs())
	}
}

func TestACatalogButtonAfterARestartFindsTheTasksUnit(t *testing.T) {
	r := newRig(t)
	r.d.tasks["next"] = nil
	r.d.tasks["inbox"] = nil
	r.d.tasks["someday"] = []api.TaskView{pendingTask(3)}
	resp, _ := ResponseFor(pendingTask(3).Attention.ID, "hold")
	r.c.Respond(context.Background(), resp)
	if got := r.d.snapshotActions(); !slices.Equal(got, []string{"hold acme/api#3"}) {
		t.Fatalf("actions %q", got)
	}
}

func TestOpenGoesToTheTask(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.c.Respond(ctx, Response{ID: pendingTask(1).Attention.ID, Action: ActionOpen})
	r.c.Respond(ctx, Response{ID: questionTask(6).Attention.ID, Action: ActionOpen})
	want := []string{"cockpit#task=e-1", "cockpit#task=e-6"}
	waitFor(t, func() bool { return slices.Equal(r.openedURLs(), want) })
	if len(r.d.snapshotActions())+len(r.d.snapshotVerbs()) != 0 {
		t.Fatalf("opening posted %q %+v", r.d.snapshotActions(), r.d.snapshotVerbs())
	}
}

func TestOutboxOpenGoesToTheDraftTask(t *testing.T) {
	r := newRig(t)
	r.d.tasks["outbox"] = []api.TaskView{draftTask(7)}
	r.c.Refresh(context.Background())
	resp, _ := ResponseFor(r.notes.posted[0].ID, "open")
	r.c.Respond(context.Background(), resp)
	if o := r.openedURLs(); !slices.Equal(o, []string{"cockpit#task=draft:7"}) {
		t.Fatalf("opened %v", o)
	}
	if len(r.d.snapshotActions()) != 0 {
		t.Fatalf("a draft's open posted %q", r.d.snapshotActions())
	}
}

func TestADraftNotificationNeverApproves(t *testing.T) {
	r := newRig(t)
	d := draftTask(7)
	d.Attention.Actions = append([]api.AttentionAction{{ID: "task:approve", Title: "Approve"}}, d.Attention.Actions...)
	r.d.tasks["outbox"] = []api.TaskView{d}
	r.c.Refresh(context.Background())
	n := r.notes.posted[0]
	if got := buttons(n); !slices.Equal(got, []string{"open Open", "task:dismiss Discard"}) {
		t.Fatalf("a draft offers %q", got)
	}
	for _, action := range []string{"task:approve", "task:dismiss"} {
		resp, _ := ResponseFor(n.ID, action)
		r.c.Respond(context.Background(), resp)
	}
	if v := r.d.snapshotVerbs(); len(v) != 1 || v[0].id != "draft:7" || v[0].verb != "dismiss" {
		t.Fatalf("verbs %+v", v)
	}
}

func TestQuickAnswersPostTheTasksAnswer(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id := questionTask(7).Attention.ID
	for _, c := range []struct{ action, text string }{{"answer:yes", ""}, {"answer:no", ""}, {"answer:text", "keep the retry"}} {
		resp, ok := ResponseFor(id, c.action)
		if !ok {
			t.Fatalf("%s is no response", c.action)
		}
		resp.Text = c.text
		r.c.Respond(ctx, resp)
	}
	var got []string
	for _, v := range r.d.snapshotVerbs() {
		got = append(got, fmt.Sprintf("%s %s %v %s", v.id, v.verb, v.req.Args, v.req.Source))
	}
	want := []string{"e-7 answer map[yes:true] notification", "e-7 answer map[no:true] notification", "e-7 answer map[guidance:keep the retry] notification"}
	if !slices.Equal(got, want) {
		t.Fatalf("answers %q\nwant    %q", got, want)
	}
	for _, unknown := range []string{"", "answer:option:", "com.apple.UNNotificationDismissActionIdentifier"} {
		if _, ok := ResponseFor(id, unknown); ok {
			t.Errorf("%q became a response", unknown)
		}
	}
}

func TestAnAnswerThatDidNotLandSaysSo(t *testing.T) {
	r := newRig(t)
	r.d.failing = errors.New("no agent session in the workspace of acme/api#5 to answer; use Review or Address")
	resp, _ := ResponseFor(questionTask(5).Attention.ID, "answer:text")
	resp.Text = "sim"
	r.c.Respond(context.Background(), resp)
	if len(r.notes.posted) != 1 {
		t.Fatalf("posted %+v", r.notes.posted)
	}
	n := r.notes.posted[0]
	cat, route, ok := api.ParseNotificationID(n.ID)
	if !ok || cat != api.CategoryNote || route != "task=e-5" || n.Title != "Answer not sent" || !strings.Contains(n.Body, "no agent session") {
		t.Fatalf("%+v", n)
	}
	r.c.Respond(context.Background(), Response{ID: n.ID, Action: ActionOpen})
	if o := r.openedURLs(); !slices.Equal(o, []string{"cockpit#task=e-5"}) {
		t.Fatalf("the note opened %v", o)
	}
}

func TestAnActionThatDidNotLandSaysSo(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(7)}
	r.c.Refresh(context.Background())
	r.d.failing = errors.New("the row changed: the pending round already went out")
	for _, action := range []string{"hold", "task:approve"} {
		resp, _ := ResponseFor(pendingTask(7).Attention.ID, action)
		r.c.Respond(context.Background(), resp)
	}
	notes := r.notes.snapshot()[1:]
	if len(notes) != 2 || notes[0].Title != "Action not sent" || notes[1].Title != "Action not sent" || notes[0].Subtitle != "acme/api#7" ||
		!strings.Contains(notes[0].Body, "the row changed") {
		t.Fatalf("posted %+v", notes)
	}
}

func TestAQueuedAnswerSaysItWaitsForTheAgent(t *testing.T) {
	r := newRig(t)
	r.d.result = api.TaskResult{Lines: []string{api.AnswerQueued}}
	resp, _ := ResponseFor(questionTask(7).Attention.ID, "answer:yes")
	r.c.Respond(context.Background(), resp)
	if len(r.notes.posted) != 1 || r.notes.posted[0].Title != "Answer queued" || r.notes.posted[0].Body != api.AnswerQueued {
		t.Fatalf("posted %+v", r.notes.posted)
	}
	r.d.result = api.TaskResult{Lines: []string{"answer sent to the agent session"}}
	r.c.Respond(context.Background(), resp)
	if len(r.notes.posted) != 1 {
		t.Fatalf("a typed answer posted %+v", r.notes.posted[1:])
	}
}

func TestAnUnknownIDIsIgnored(t *testing.T) {
	r := newRig(t)
	for _, id := range []string{"Q5|acme|api|30|5", "ROUND|acme|api|1|x", "garbage"} {
		resp, _ := ResponseFor(id, "answer:yes")
		r.c.Respond(context.Background(), resp)
		r.c.Respond(context.Background(), Response{ID: id, Action: ActionOpen})
	}
	if len(r.d.snapshotVerbs())+len(r.d.snapshotActions())+len(r.openedURLs())+len(r.notes.posted) != 0 {
		t.Fatalf("an old id did something: %+v %q %v %+v", r.d.snapshotVerbs(), r.d.snapshotActions(), r.openedURLs(), r.notes.posted)
	}
}

func TestEachNotificationResponseIsLoggedWithoutItsText(t *testing.T) {
	r := newRig(t)
	var logs []string
	r.c.cfg.Log = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	id := questionTask(7).Attention.ID
	resp, _ := ResponseFor(id, "answer:text")
	resp.Text = "look at the retry"
	r.c.Respond(context.Background(), resp)
	want := fmt.Sprintf(`notification response %s: action "answer:text" as answer, option "", yes false, text of 17 bytes`, id)
	if len(logs) != 1 || logs[0] != want {
		t.Fatalf("logs %q\nwant %q", logs, want)
	}
}

func TestNotificationButtonsSendTheBasisTheyWereServedFor(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(1)}
	r.c.Refresh(context.Background())
	resp, _ := ResponseFor(r.notes.posted[0].ID, "hold")
	r.c.Respond(context.Background(), resp)
	if reqs := r.d.snapshotRequests(); len(reqs) != 1 || reqs[0].req.Basis != "b-hold" {
		t.Fatalf("requests %+v", reqs)
	}
}

func TestEveryPostCarriesItsSource(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	rows := goldenRows(t)
	r.d.rows["loop"] = []api.Row{rows["pending"], rows["asked_choice"], rows["intake_waiting_cli"]}
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(1), questionTask(2)}
	r.d.tasks["outbox"] = []api.TaskView{draftTask(3)}
	r.c.Refresh(ctx)
	var clicks int
	for _, it := range r.tray.menu {
		for _, child := range it.Children {
			if child.Action != nil {
				child.Action()
				clicks++
			}
		}
	}
	opens := 0
	for _, n := range r.notes.snapshot() {
		for _, b := range n.Actions {
			resp, ok := ResponseFor(n.ID, b.ID)
			if !ok {
				t.Fatalf("%s is no response", b.ID)
			}
			resp.Text = "text"
			r.c.Respond(ctx, resp)
			if cat, _, _ := api.ParseNotificationID(n.ID); cat == api.CategoryOutbox && b.ID == "open" {
				opens++
				continue
			}
			clicks++
		}
	}
	waitFor(t, func() bool { return len(r.d.snapshotRequests())+len(r.d.snapshotVerbs()) == clicks })
	for _, q := range r.d.snapshotRequests() {
		if q.req.Source != "menubar" {
			t.Errorf("%s posted with source %q", q.name, q.req.Source)
		}
	}
	for _, v := range r.d.snapshotVerbs() {
		want := "menubar"
		if v.verb == "answer" {
			want = AnswerSource
		}
		if v.req.Source != want {
			t.Errorf("%s posted with source %q", v.verb, v.req.Source)
		}
	}
	if clicks < 12 || opens != 1 {
		t.Fatalf("only %d posts and %d opens exercised", clicks, opens)
	}
}

func TestAFailedPostIsRetried(t *testing.T) {
	r := newRig(t)
	r.notes.postErr = errors.New("not authorized")
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(1)}
	r.c.Refresh(context.Background())
	r.notes.postErr = nil
	r.c.Refresh(context.Background())
	if len(r.notes.posted) != 1 {
		t.Fatalf("posted %d", len(r.notes.posted))
	}
}

func TestDeliveredNotificationsAreClearedAtStart(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.c.Run(ctx); close(done) }()
	waitFor(t, func() bool { return len(r.notes.snapshot()) == 1 })
	cancel()
	<-done
	r.notes.mu.Lock()
	defer r.notes.mu.Unlock()
	if r.notes.cleared != 1 {
		t.Fatalf("cleared %d times", r.notes.cleared)
	}
}

func TestUnreadableIdsAreWithdrawnAtStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	today := now.Local().Format(time.DateOnly)
	unreadable := "HELD|acme|api|4|" + today
	writeSent(t, path, sentFile{Day: today, IDs: []string{unreadable}})
	notes := &fakeNotifier{perm: PermissionAuthorized}
	d := newDaemon()
	d.tasks["inbox"] = []api.TaskView{heldTask(4, now)}
	c := newController(d, notes, path, func() time.Time { return now })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	waitFor(t, func() bool { return len(notes.snapshot()) == 1 })
	cancel()
	<-done
	notes.mu.Lock()
	cleared := notes.cleared
	notes.mu.Unlock()
	if cleared != 1 {
		t.Fatalf("the delivered notifications were cleared %d times at start", cleared)
	}
	if f := readSent(t, path); slices.Contains(f.IDs, unreadable) || !slices.Equal(f.IDs, []string{heldTask(4, now).Attention.ID}) {
		t.Fatalf("the sent file holds %q", f.IDs)
	}
}

func writeSent(t *testing.T, path string, f sentFile) {
	t.Helper()
	raw, _ := json.Marshal(f)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readSent(t *testing.T, path string) sentFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	var f sentFile
	if err == nil {
		err = json.Unmarshal(raw, &f)
	}
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return f
}

func newController(d *fakeDaemon, notes *fakeNotifier, path string, clock func() time.Time) *Controller {
	return New(Config{
		Daemon: d, Tray: &fakeTray{}, Notifier: notes, SentPath: path,
		OpenURL: func(string) error { return nil }, CockpitURL: func(string) (string, error) { return "", nil }, Quit: func() {},
		Now: clock,
	})
}

func startAt(t *testing.T, path string, perm Permission, at time.Time, tasks ...api.TaskView) *fakeNotifier {
	t.Helper()
	notes := &fakeNotifier{perm: perm}
	d := newDaemon()
	d.tasks["inbox"] = tasks
	newController(d, notes, path, func() time.Time { return at }).Refresh(context.Background())
	return notes
}

func TestHeldTaskOncePerDayAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	if n := startAt(t, path, PermissionAuthorized, now, heldTask(4, now)); len(n.posted) != 1 {
		t.Fatalf("first start posted %d", len(n.posted))
	}
	if n := startAt(t, path, PermissionAuthorized, now.Add(time.Minute), heldTask(4, now)); len(n.posted) != 0 {
		t.Fatalf("restart posted again: %+v", n.posted)
	}
	next := now.Add(24 * time.Hour)
	if n := startAt(t, path, PermissionAuthorized, next, heldTask(4, next)); len(n.posted) != 1 {
		t.Fatalf("next day posted %d", len(n.posted))
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := now.Add(48 * time.Hour)
	if n := startAt(t, path, PermissionAuthorized, later, heldTask(4, later)); len(n.posted) != 1 {
		t.Fatalf("a broken file stopped the notification: %d", len(n.posted))
	}
}

func TestTheSentFileKeepsOnlyTodaysHeldTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	tasks := []api.TaskView{heldTask(4, now), waitTask(5, "a"), pendingTask(6)}
	if n := startAt(t, path, PermissionAuthorized, now, tasks...); len(n.posted) != 3 {
		t.Fatalf("posted %+v", n.posted)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi, err)
	}
	if f := readSent(t, path); f.Day != now.Local().Format(time.DateOnly) || !slices.Equal(f.IDs, []string{tasks[0].Attention.ID}) {
		t.Fatalf("%+v", f)
	}
	if n := startAt(t, path, PermissionAuthorized, now, tasks...); len(n.posted) != 2 || slices.ContainsFunc(n.posted, func(p Notification) bool { cat, _, _ := api.ParseNotificationID(p.ID); return cat == api.CategoryHeld }) {
		t.Fatalf("after a restart: %+v", n.posted)
	}
}

func TestTheSentFileIsReadOnlyForItsDayAndForHeldTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	today := now.Local().Format(time.DateOnly)
	writeSent(t, path, sentFile{Day: now.Add(-24 * time.Hour).Local().Format(time.DateOnly), IDs: []string{heldTask(4, now).Attention.ID}})
	if n := startAt(t, path, PermissionAuthorized, now, heldTask(4, now)); len(n.posted) != 1 {
		t.Fatalf("yesterday's file suppressed today's notice: %+v", n.posted)
	}
	wait := waitTask(5, "a")
	writeSent(t, path, sentFile{Day: today, IDs: []string{wait.Attention.ID}})
	if n := startAt(t, path, PermissionAuthorized, now, wait); len(n.posted) != 1 {
		t.Fatalf("the file suppressed a wait: %+v", n.posted)
	}
}

func TestAHeldTaskWaitsForNotificationsToBeAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	if n := startAt(t, path, PermissionDenied, now, heldTask(4, now)); len(n.posted) != 0 {
		t.Fatalf("posted while denied: %+v", n.posted)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("saved a notice nobody saw")
	}
	if n := startAt(t, path, PermissionAuthorized, now, heldTask(4, now)); len(n.posted) != 1 {
		t.Fatalf("allowed later the same day: %+v", n.posted)
	}
}

func TestAHeldTaskAlreadyPostedDoesNotAskForPermission(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{heldTask(4, now)}
	r.c.Refresh(context.Background())
	before := r.notes.permissionCalls()
	r.c.Refresh(context.Background())
	withHeld := r.notes.permissionCalls() - before
	r.d.tasks["inbox"] = nil
	r.c.Refresh(context.Background())
	before = r.notes.permissionCalls()
	r.c.Refresh(context.Background())
	if without := r.notes.permissionCalls() - before; withHeld != without {
		t.Fatalf("a held task posted already asked for permission: %d calls, %d without it", withHeld, without)
	}
}

func TestARestartThroughRunPostsTodaysHeldTaskAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	if n := startAt(t, path, PermissionAuthorized, now, heldTask(4, now)); len(n.posted) != 1 {
		t.Fatalf("first start posted %d", len(n.posted))
	}
	notes := &fakeNotifier{perm: PermissionAuthorized}
	d := newDaemon()
	d.tasks["inbox"] = []api.TaskView{heldTask(4, now)}
	c := newController(d, notes, path, func() time.Time { return now })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	waitFor(t, func() bool { return len(notes.snapshot()) == 1 })
	cancel()
	<-done
	notes.mu.Lock()
	defer notes.mu.Unlock()
	if notes.cleared != 1 || len(notes.posted) != 1 {
		t.Fatalf("cleared %d, posted %d", notes.cleared, len(notes.posted))
	}
}

func TestACategoryIsRegisteredFromTheServedButtons(t *testing.T) {
	c := NewCategories()
	a := pendingTask(1).Attention
	n := Notification{ID: a.ID, Category: Category(a.Category)}
	for _, x := range a.Actions {
		n.Actions = append(n.Actions, NotificationAction{ID: x.ID, Title: x.Title, TextInput: x.TextInput})
	}
	if !c.Post(n) || c.Post(n) {
		t.Fatalf("the category follows the served buttons: registered %+v, posted %+v", c.List(), n.Actions)
	}
}

func TestAQuestionCategoryStaysWhileAnotherNotificationUsesIt(t *testing.T) {
	c := NewCategories()
	actions := []NotificationAction{{ID: "task:dismiss", Title: "Dismiss"}}
	a := Notification{ID: api.NotificationID("Q0", "e-1", "0"), Category: "Q0", Actions: actions}
	b := Notification{ID: api.NotificationID("Q0", "e-2", "0"), Category: "Q0", Actions: actions}
	if !c.Post(a) || c.Post(b) {
		t.Fatal("the second post of the same category changed the set")
	}
	has := func() bool {
		return slices.ContainsFunc(c.List(), func(s CategorySpec) bool { return s.ID == "Q0" })
	}
	if c.Withdraw([]string{a.ID}) || !has() {
		t.Fatal("dropped Q0 while another notification uses it")
	}
	if !c.Withdraw([]string{b.ID}) || has() {
		t.Fatal("kept Q0 after its last notification")
	}
	if c.Withdraw([]string{api.NotificationID(api.CategoryInbox, "e-3", "x")}) {
		t.Fatal("an Inbox task's withdrawal changed the categories")
	}
}

func TestMenubarNotifiesFromTheSet(t *testing.T) {
	r := newRig(t)
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(1)}
	r.c.Refresh(context.Background())
	r.c.Refresh(context.Background())
	if n := len(r.notes.snapshot()); n != 1 {
		t.Fatalf("posted %d notifications for one task over two refreshes", n)
	}
}

func TestMenubarNotificationsFollowTheInbox(t *testing.T) {
	r := newRig(t)
	r.c.Refresh(context.Background())
	added := pendingTask(1)
	inbox := func(tasks ...api.TaskView) {
		r.d.mu.Lock()
		r.d.tasks["inbox"] = tasks
		r.d.mu.Unlock()
		r.c.Refresh(context.Background())
	}
	inbox(added)
	if len(r.notes.postedIDs()) != 1 {
		t.Fatalf("a task added posted %v", r.notes.postedIDs())
	}
	inbox(added)
	if len(r.notes.postedIDs()) != 1 {
		t.Fatalf("the same Inbox again posted %v", r.notes.postedIDs())
	}
	inbox()
	if w := r.notes.withdrawnIDs(); len(w) != 1 || w[0] != added.Attention.ID {
		t.Fatalf("the task leaving withdrew %v", w)
	}
}

func TestMenubarReconnectPostsNothing(t *testing.T) {
	r, k := clockedRig(t)
	r.d.events = make(chan api.Change)
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(1)}
	stop := run(t, r)
	defer stop()
	waitFor(t, func() bool { return len(r.notes.postedIDs()) == 1 })
	close(r.d.events)
	waitFor(t, func() bool { return len(k.waited()) == 1 })
	k.advance(refreshMin)
	waitFor(t, func() bool { rows, _ := r.d.counts(); return rows == 2 })
	if got := r.notes.postedIDs(); len(got) != 1 {
		t.Fatalf("a reconnect posted %v", got)
	}
}

func TestMenubarPostsANoticeWithoutPullingTheRows(t *testing.T) {
	r, stop := healthOnlyRig(t)
	defer stop()
	r.d.events <- api.Change{Type: "notify", Epoch: "e1", Rev: 1, Notices: []api.Notice{{ID: "ping@1", Workflow: "ping", Unit: uref(7), Text: "look at it"}}}
	var got Notification
	waitFor(t, func() bool {
		for _, n := range r.notes.snapshot() {
			if n.Body == "look at it" {
				got = n
				return true
			}
		}
		return false
	})
	if got.Category != api.CategoryNote || got.Title != "ping" || got.Subtitle != "acme/api#7" {
		t.Fatalf("posted %+v", got)
	}
	if rows, _ := r.d.counts(); rows != 1 {
		t.Fatalf("%d rows pulls, want the boot one only", rows)
	}
	r.c.Respond(context.Background(), Response{ID: got.ID, Action: ActionOpen})
	if o := r.openedURLs(); !slices.Equal(o, []string{"cockpit#unit=github/acme%2Fapi%237"}) {
		t.Fatalf("the notice opened %v", o)
	}
}

func TestEachActionSetGetsItsOwnCategory(t *testing.T) {
	r := newRig(t)
	plain := plainTask(3)
	plain.Attention = &api.Attention{ID: api.NotificationID(api.CategoryInbox, plain.ID, "x"), Category: "INBOX", Title: "In your Inbox", Body: plain.Title, Actions: []api.AttentionAction{}}
	r.d.tasks["inbox"] = []api.TaskView{pendingTask(1), waitTask(2, "a"), plain, questionTask(4)}
	r.c.Refresh(context.Background())
	posted := r.notes.snapshot()
	if len(posted) != 4 {
		t.Fatalf("posted %+v", posted)
	}
	cats := map[Category]bool{}
	for _, n := range posted {
		if got := r.notes.categoryActions(n.Category); !slices.Equal(got, n.Actions) {
			t.Errorf("%s shows %+v under %s, posted with %+v", n.ID, got, n.Category, n.Actions)
		}
		cats[n.Category] = true
	}
	if len(cats) != 4 || !cats["Q5"] {
		t.Fatalf("categories %v", cats)
	}
	r.d.tasks["inbox"] = nil
	r.c.Refresh(context.Background())
	if got := r.notes.categories(); len(got) != 0 {
		t.Fatalf("categories outlive their notifications: %q", got)
	}
}

func TestAttentionChangedWithoutATaskWritePostsTheNewID(t *testing.T) {
	r := newRig(t)
	e := waitTask(5, "a")
	r.d.tasks["inbox"] = []api.TaskView{e}
	r.c.Refresh(context.Background())
	proposed := waitTask(5, "with-draft-1")
	r.d.mu.Lock()
	r.d.tasks["inbox"] = []api.TaskView{proposed}
	r.d.mu.Unlock()
	r.c.Refresh(context.Background())
	if got := r.notes.postedIDs(); !slices.Equal(got, []string{e.Attention.ID, proposed.Attention.ID}) {
		t.Fatalf("posted %q", got)
	}
	if w := r.notes.withdrawnIDs(); !slices.Equal(w, []string{e.Attention.ID}) {
		t.Fatalf("withdrawn %q", w)
	}
}

func TestANoticeIDWithABarStillOpens(t *testing.T) {
	r := newRig(t)
	r.c.postNotice(api.Notice{ID: "odd|flow@1", Workflow: "odd|flow", Unit: uref(7), Text: "look"})
	n := r.notes.snapshot()[0]
	if _, _, ok := api.ParseNotificationID(n.ID); !ok {
		t.Fatalf("id %q does not parse", n.ID)
	}
	r.c.Respond(context.Background(), Response{ID: n.ID, Action: ActionOpen})
	if o := r.openedURLs(); !slices.Equal(o, []string{"cockpit#unit=github/acme%2Fapi%237"}) {
		t.Fatalf("opened %v", o)
	}
}

func TestConcurrentSavesKeepEveryID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	c := newController(newDaemon(), &fakeNotifier{perm: PermissionAuthorized}, path, func() time.Time { return now })
	day := now.Local().Format(time.DateOnly)
	var wg sync.WaitGroup
	want := []string{}
	for i := range 64 {
		id := api.NotificationID(api.CategoryHeld, taskID(i), day)
		want = append(want, id)
		wg.Go(func() {
			c.mu.Lock()
			c.daily[id] = true
			c.mu.Unlock()
			c.saveSent(day)
		})
	}
	wg.Wait()
	slices.Sort(want)
	if f := readSent(t, path); !slices.Equal(f.IDs, want) {
		t.Fatalf("the sent file holds %d of %d ids", len(f.IDs), len(want))
	}
}

func TestNotificationActionsUseTaskIds(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	e := pendingTask(1)
	e.Attention.Actions = []api.AttentionAction{{ID: "task:approve", Title: "Run now"}, {ID: "task:delegate:headless", Title: "Draft for me"}, {ID: "gtd:dismiss", Title: "Skip"}}
	r.d.tasks["inbox"] = []api.TaskView{e}
	r.d.tasks["outbox"] = []api.TaskView{draftTask(7)}
	r.c.Refresh(ctx)
	for _, action := range []string{"task:approve", "task:delegate:headless", "gtd:dismiss"} {
		resp, _ := ResponseFor(e.Attention.ID, action)
		r.c.Respond(ctx, resp)
	}
	var verbs []string
	for _, v := range r.d.snapshotVerbs() {
		verbs = append(verbs, fmt.Sprintf("%s %s %v", v.id, v.verb, v.req.Args))
	}
	if want := []string{"e-1 approve map[]", "e-1 delegate map[mode:headless]"}; !slices.Equal(verbs, want) {
		t.Fatalf("verbs %q, want %q: an old gtd: id must not run a verb", verbs, want)
	}
	var draft Notification
	for _, n := range r.notes.posted {
		if cat, _, _ := api.ParseNotificationID(n.ID); cat == api.CategoryOutbox {
			draft = n
		}
	}
	if got := buttons(draft); !slices.Equal(got, []string{"open Open", "task:dismiss Discard"}) {
		t.Fatalf("a draft offers %q", got)
	}
	r.d.failing = errors.New("refused")
	resp, _ := ResponseFor(e.Attention.ID, "task:approve")
	r.c.Respond(ctx, resp)
	note := r.notes.snapshot()[len(r.notes.snapshot())-1]
	if _, route, ok := api.ParseNotificationID(note.ID); !ok || route != "task=e-1" {
		t.Fatalf("the note's id %q does not carry the task route", note.ID)
	}
}
