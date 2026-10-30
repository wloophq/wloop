package menubar

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func inboxNoteID(n int) string {
	return api.NotificationID(api.CategoryInbox, taskID(n), "tok")
}

func TestAReplyTypedInTheNotificationGoesToTheOutboxOrOut(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id := inboxNoteID(8)
	for _, c := range []struct{ action, text string }{{api.AttentionReply, "Friday works"}, {api.AttentionSendReply, "Shipped"}} {
		resp, ok := ResponseFor(id, c.action)
		if !ok || resp.Action != ActionReply {
			t.Fatalf("%s: %+v %v", c.action, resp, ok)
		}
		resp.Text = c.text
		r.d.result = api.TaskResult{Lines: []string{"draft 3 saved to the Outbox for your approval"}}
		r.c.Respond(ctx, resp)
	}
	var got []string
	for _, v := range r.d.snapshotVerbs() {
		got = append(got, fmt.Sprintf("%s %s %v %s", v.id, v.verb, v.req.Args, v.req.Source))
	}
	want := []string{"e-8 reply map[send:draft text:Friday works] notification", "e-8 reply map[send:now text:Shipped] notification"}
	if !slices.Equal(got, want) {
		t.Fatalf("replies %q\nwant    %q", got, want)
	}
	notes := r.notes.snapshot()
	if len(notes) != 2 || notes[0].Title != "Reply saved to the Outbox" || notes[0].Body != "draft 3 saved to the Outbox for your approval" || notes[1].Title != "Reply sent" {
		t.Fatalf("notes %+v", notes)
	}
}

func TestAnEmptyReplyIsNotSaved(t *testing.T) {
	r := newRig(t)
	resp, _ := ResponseFor(inboxNoteID(8), api.AttentionReply)
	r.c.Respond(context.Background(), resp)
	if v := r.d.snapshotVerbs(); len(v) != 0 {
		t.Fatalf("verbs %+v", v)
	}
	if notes := r.notes.snapshot(); len(notes) != 1 || notes[0].Title != "Reply not saved" {
		t.Fatalf("notes %+v", notes)
	}
}

func TestDraftForMeDelegatesHeadless(t *testing.T) {
	r := newRig(t)
	resp, ok := ResponseFor(inboxNoteID(8), api.AttentionDraftForMe)
	if !ok || resp.Action != ActionServed {
		t.Fatalf("%+v %v", resp, ok)
	}
	r.c.Respond(context.Background(), resp)
	v := r.d.snapshotVerbs()
	if len(v) != 1 || v[0].id != "e-8" || v[0].verb != "delegate" || v[0].req.Args["mode"] != "headless" || v[0].req.Source != source {
		t.Fatalf("verbs %+v", v)
	}
}

func TestTheNotificationOffersTheServedReplyFirst(t *testing.T) {
	for _, first := range []string{api.AttentionSendReply, api.AttentionReply} {
		other := api.AttentionReply
		if first == api.AttentionReply {
			other = api.AttentionSendReply
		}
		e := api.TaskView{ID: "e-8", Attention: &api.Attention{ID: "n1", Category: string(api.CategoryInbox), Title: "In your Inbox",
			Actions: []api.AttentionAction{{ID: first, Title: "first", TextInput: true, Placeholder: "p1", Button: "b1"}, {ID: other, Title: "second", TextInput: true}}}}
		n, _ := notification(e, nil)
		if len(n.Actions) != 2 || n.Actions[0].ID != first || n.Actions[0].Placeholder != "p1" || n.Actions[0].Button != "b1" {
			t.Fatalf("%s first: %+v", first, n.Actions)
		}
	}
}
