package slack

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	dmTop      = "1700000000.000100"
	dmRoot     = "1700000100.000100"
	dmReply    = "1700000100.000200"
	dmOtherTop = "1700000200.000100"
	dmLater    = "1700000200.000300"
)

// dmThreads answers conversations.replies for the DM: each ts names the
// thread_ts it lives in, "" for a top-level message with no replies.
func (ps *pubSlack) dmThreads(threadOf map[string]string) {
	ps.f.api.Handle("conversations.replies", func(v url.Values) any {
		ts := v.Get("ts")
		m := map[string]any{"ts": ts, "text": "message " + ts, "user": other}
		if th := threadOf[ts]; th != "" {
			if th != ts {
				m = map[string]any{"ts": th, "thread_ts": th, "text": "message " + th, "user": other}
			} else {
				m["thread_ts"] = th
			}
		}
		return map[string]any{"messages": []map[string]any{m}}
	})
}

func anchored(u unit.Unit, id string, refs ...string) items.Item {
	it := items.Item{ID: id, Unit: u, Origin: unit.Anchor{Unit: u, Ref: refs[0]}}
	for _, r := range refs[1:] {
		it.Trail = append(it.Trail, items.Evidence{Kind: items.EvidenceMessage, Anchor: unit.Anchor{Unit: u, Ref: r}})
	}
	return it
}

func (ps *pubSlack) placing(its ...items.Item) *Publisher {
	p := ps.publisher()
	p.Items = func(context.Context, unit.Unit) ([]items.Item, error) { return its, nil }
	return p
}

func placeAndPublish(t *testing.T, p *Publisher, d draft.Draft) draft.Draft {
	t.Helper()
	target, err := p.Place(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	d.Target = target
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAReplyToATopLevelDMMessageStartsItsThread(t *testing.T) {
	ps := newPubSlack(t)
	ps.dmThreads(nil)
	seg := ps.segmentUnit(t)
	d := placeAndPublish(t, ps.placing(anchored(seg, "i1", pubDM+"/"+dmTop)),
		draft.Draft{ID: 1, Unit: seg, Items: []string{"i1"}, Kind: draft.Close, Text: "Closing this out"})
	if !strings.Contains(d.Target, pubDM) || !strings.Contains(d.Target, "p1700000000000100") {
		t.Fatalf("target %q", d.Target)
	}
	if post := ps.posted(t); post.Get("channel") != pubDM || post.Get("thread_ts") != dmTop {
		t.Fatalf("post %v", post)
	}
}

func TestAReplyToADMThreadJoinsIt(t *testing.T) {
	ps := newPubSlack(t)
	ps.dmThreads(map[string]string{dmRoot: dmRoot, dmReply: dmRoot})
	seg := ps.segmentUnit(t)
	placeAndPublish(t, ps.placing(anchored(seg, "i1", pubDM+"/"+dmRoot, pubDM+"/"+dmReply)),
		draft.Draft{ID: 1, Unit: seg, Items: []string{"i1"}, Kind: draft.Reply, Text: "Who owns this?"})
	if post := ps.posted(t); post.Get("channel") != pubDM || post.Get("thread_ts") != dmRoot {
		t.Fatalf("post %v", post)
	}
}

func TestItemsAcrossThreadsPlaceInTheNewestAnchorsThread(t *testing.T) {
	ps := newPubSlack(t)
	ps.dmThreads(map[string]string{dmRoot: dmRoot, dmReply: dmRoot, dmOtherTop: dmOtherTop, dmLater: dmOtherTop})
	seg := ps.segmentUnit(t)
	p := ps.placing(
		anchored(seg, "i1", pubDM+"/"+dmRoot, pubDM+"/"+dmReply),
		anchored(seg, "i2", pubDM+"/"+dmLater, "draft:9"),
		anchored(seg, "i3", pubDM+"/1700000900.000100"),
	)
	placeAndPublish(t, p, draft.Draft{ID: 1, Unit: seg, Items: []string{"i1", "i2"}, Kind: draft.Reply, Text: "Who owns these?"})
	if post := ps.posted(t); post.Get("thread_ts") != dmOtherTop {
		t.Fatalf("post %v", post)
	}
}

func TestAChannelThreadKeepsItsThread(t *testing.T) {
	ps := newPubSlack(t)
	d := threadDraft(draft.Reply, "on it")
	d.Items = []string{"i1"}
	p := ps.placing(anchored(d.Unit, "i1", pubConv+"/"+pubReply))
	if target, err := p.Place(context.Background(), d); err != nil || target != "" {
		t.Fatalf("Place = %q %v", target, err)
	}
}

func TestASegmentDraftWithNothingToAnswerIsRefused(t *testing.T) {
	ps := newPubSlack(t)
	seg := ps.segmentUnit(t)
	for name, tc := range map[string]struct {
		items []string
		its   []items.Item
		want  string
	}{
		"no items":           {want: "neither a target nor items"},
		"no message":         {items: []string{"i1"}, its: []items.Item{anchored(seg, "i1", "draft:3")}, want: "none of the items"},
		"other conversation": {items: []string{"i1"}, its: []items.Item{anchored(seg, "i1", "D0000000999/"+dmTop)}, want: "none of the items"},
	} {
		t.Run(name, func(t *testing.T) {
			d := draft.Draft{Unit: seg, Items: tc.items, Kind: draft.Reply, Text: "hi"}
			if _, err := ps.placing(tc.its...).Place(context.Background(), d); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Place = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAnUnplacedSegmentDraftIsNotPublished(t *testing.T) {
	ps := newPubSlack(t)
	d := draft.Draft{ID: 1, Unit: ps.segmentUnit(t), Kind: draft.Reply, Text: "done"}
	if _, err := ps.publisher().Publish(context.Background(), d); err == nil || !strings.Contains(err.Error(), "target") {
		t.Fatalf("Publish = %v", err)
	}
	if n := len(ps.calls("chat.postMessage")); n != 0 {
		t.Fatalf("posted %d", n)
	}
}
