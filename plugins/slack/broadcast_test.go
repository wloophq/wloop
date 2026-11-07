package slack

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/draft"
)

func placeOnly(t *testing.T, p *Publisher, d draft.Draft) draft.Draft {
	t.Helper()
	got, err := p.Place(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if d.Target == "" {
		d.Target = got.Target
	}
	d.Broadcast = got.Broadcast
	return d
}

func TestADMThreadReplyBroadcastsByDefault(t *testing.T) {
	for _, kind := range []draft.Kind{draft.Reply, draft.Nudge, draft.Close} {
		t.Run(string(kind), func(t *testing.T) {
			ps := newPubSlack(t)
			ps.dmThreads(nil)
			seg := ps.segmentUnit(t)
			d := placeAndPublish(t, ps.placing(anchored(seg, "i1", pubDM+"/"+dmTop)), segmentDraft(seg, kind, draft.OriginAuto, "i1"))
			if d.Broadcast != draft.BroadcastOn {
				t.Fatalf("broadcast %q", d.Broadcast)
			}
			if post := ps.posted(t); post.Get("channel") != pubDM || post.Get("thread_ts") != dmTop || post.Get("reply_broadcast") != "true" {
				t.Fatalf("post %v", post)
			}
		})
	}
}

func TestADMThreadReplyTheUserTurnedOffStaysInItsThread(t *testing.T) {
	ps := newPubSlack(t)
	ps.dmThreads(nil)
	seg := ps.segmentUnit(t)
	p := ps.placing(anchored(seg, "i1", pubDM+"/"+dmTop))
	d := placeOnly(t, p, segmentDraft(seg, draft.Reply, draft.OriginAuto, "i1"))
	d.Broadcast = draft.BroadcastOff
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if post := ps.posted(t); post.Get("thread_ts") != dmTop || post.Has("reply_broadcast") {
		t.Fatalf("post %v", post)
	}
}

func TestAChannelThreadReplyDoesNotBroadcastByDefault(t *testing.T) {
	ps := newPubSlack(t)
	p := ps.publisher()
	d := placeOnly(t, p, threadDraft(draft.Reply, "on it"))
	if d.Broadcast != draft.BroadcastOff {
		t.Fatalf("broadcast %q", d.Broadcast)
	}
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if post := ps.posted(t); post.Get("thread_ts") != pubParent || post.Has("reply_broadcast") {
		t.Fatalf("post %v", post)
	}
}

func TestAChannelThreadReplyBroadcastsWhenAsked(t *testing.T) {
	ps := newPubSlack(t)
	p := ps.publisher()
	d := threadDraft(draft.Reply, "on it")
	d.Broadcast = draft.BroadcastOn
	d = placeOnly(t, p, d)
	if d.Broadcast != draft.BroadcastOn {
		t.Fatalf("broadcast %q", d.Broadcast)
	}
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if post := ps.posted(t); post.Get("thread_ts") != pubParent || post.Get("reply_broadcast") != "true" {
		t.Fatalf("post %v", post)
	}
}

func TestADMThreadReplyAnAgentAskedNotToBroadcastKeepsIt(t *testing.T) {
	ps := newPubSlack(t)
	ps.dmThreads(nil)
	seg := ps.segmentUnit(t)
	d := segmentDraft(seg, draft.Reply, draft.OriginTurn, "i1")
	d.Broadcast = draft.BroadcastOff
	if got := placeOnly(t, ps.placing(anchored(seg, "i1", pubDM+"/"+dmTop)), d); got.Broadcast != draft.BroadcastOff {
		t.Fatalf("broadcast %q", got.Broadcast)
	}
}

func TestAnExplicitDMMessageTargetRepliesInItsThreadAndBroadcasts(t *testing.T) {
	ps := newPubSlack(t)
	ps.dmThreads(map[string]string{dmRoot: dmRoot, dmReply: dmRoot})
	p := ps.publisher()
	d := threadDraft(draft.Reply, "following up")
	d.Target = pubDM + "/" + dmReply
	d = placeOnly(t, p, d)
	if d.Target != pubDM+"/"+dmReply || d.Broadcast != draft.BroadcastOn {
		t.Fatalf("placed %q %q", d.Target, d.Broadcast)
	}
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if post := ps.posted(t); post.Get("channel") != pubDM || post.Get("thread_ts") != dmRoot || post.Get("reply_broadcast") != "true" {
		t.Fatalf("post %v", post)
	}
}

func TestOnlyAThreadReplyHasABroadcast(t *testing.T) {
	ps := newPubSlack(t)
	ps.dmThreads(nil)
	seg := ps.segmentUnit(t)
	toTheDM := segmentDraft(seg, draft.Reply, draft.OriginUser)
	toTheDM.Broadcast = draft.BroadcastOn
	reaction := segmentDraft(seg, draft.React, draft.OriginAuto, "i1")
	reaction.Broadcast = draft.BroadcastOn
	nudge := threadDraft(draft.Nudge, "any news?")
	nudge.Target = other
	nudge.Broadcast = draft.BroadcastOn
	p := ps.placing(anchored(seg, "i1", pubDM+"/"+dmTop))
	for name, d := range map[string]draft.Draft{"top of the DM": toTheDM, "reaction": reaction, "a person's DM": nudge} {
		if got := placeOnly(t, p, d); got.Broadcast != draft.NoBroadcast {
			t.Fatalf("%s: broadcast %q", name, got.Broadcast)
		}
	}
	d := placeOnly(t, p, toTheDM)
	d.Broadcast = draft.BroadcastOn
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if post := ps.posted(t); post.Has("thread_ts") || post.Has("reply_broadcast") {
		t.Fatalf("post %v", post)
	}
}
