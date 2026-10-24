package extract

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/slack"
)

func dmInput() Input {
	return Input{
		Viewer:       Viewer{ID: "U0001", Name: "Ann"},
		Now:          time.Unix(1700100000, 0).UTC(),
		Conversation: slack.Conversation{ID: "D0001", Kind: slack.KindIM, DMUser: "U0002"},
		Users:        map[string]slack.User{"U0001": {ID: "U0001", Name: "Ann"}, "U0002": {ID: "U0002", Name: "Bob"}},
		Context: []slack.Message{
			{TS: "1700000001.000000", User: "U0002", Text: "the quarterly deck is on the shared drive"},
			{TS: "1700000002.000000", User: "U0001", Text: "thanks, I will read it"},
		},
		Messages: []slack.Message{
			{TS: "1700000010.000000", User: "U0002", Text: "can you send the numbers for slide four?"},
			{TS: "1700000011.000000", ThreadTS: "1700000010.000000", User: "U0001", Text: "sure, tomorrow"},
			{TS: "1700000012.000000", User: "U0002", Text: "great"},
		},
		Segment: slack.Span{FromTS: "1700000010.000000"},
		Focus:   "1700000010.000000",
	}
}

func lineOf(t *testing.T, got, ts string) string {
	t.Helper()
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "ts="+ts) {
			return l
		}
	}
	t.Fatalf("no line for %s:\n%s", ts, got)
	return ""
}

func TestRenderFramesContextAndSegment(t *testing.T) {
	got := Render(dmInput())
	ctxAt, firstCtx := strings.Index(got, ContextHeading), strings.Index(got, "ts=1700000001.000000")
	start, end := strings.Index(got, SegmentStarts), strings.Index(got, SegmentEnds)
	first, last := strings.Index(got, "ts=1700000010.000000"), strings.Index(got, "ts=1700000012.000000")
	if ctxAt < 0 || ctxAt > firstCtx || start < firstCtx || start > first || end < last {
		t.Fatalf("context, then the segment start, the segment and its end, in that order:\n%s", got)
	}
	if l := lineOf(t, got, "1700000001.000000"); !strings.HasPrefix(l, "[c1 ") || strings.Contains(l, " new ") {
		t.Errorf("a context message is numbered c<N> and never new: %q", l)
	}
	if l := lineOf(t, got, "1700000010.000000"); !strings.HasPrefix(l, "[m1 ") || !strings.Contains(l, "] focus new Bob (U0002)") {
		t.Errorf("the segment's first message is m1, marked focus: %q", l)
	}
	if l := lineOf(t, got, "1700000011.000000"); !strings.HasPrefix(l, "[m2 ") || !strings.Contains(l, "reply to m1") || strings.Contains(l, "focus") {
		t.Errorf("segment reply = %q", l)
	}
}

func TestRenderWithoutContextOrSegmentIsUnchanged(t *testing.T) {
	in := dmInput()
	plain := in
	plain.Context, plain.Segment, plain.Focus = nil, slack.Span{}, ""
	got := Render(plain)
	for _, marker := range []string{ContextHeading, SegmentStarts, SegmentEnds, "focus", "[c"} {
		if strings.Contains(got, marker) {
			t.Errorf("a plain rendering holds %q:\n%s", marker, got)
		}
	}
	if l := lineOf(t, got, "1700000010.000000"); !strings.HasPrefix(l, "[m1 ts=1700000010.000000 ") || !strings.Contains(l, "] new Bob (U0002): can you") {
		t.Errorf("plain line = %q", l)
	}
}

func TestRenderClosesASegmentAtItsEnd(t *testing.T) {
	in := dmInput()
	in.Segment.ToTS = "1700000011.000000"
	got := Render(in)
	if end, after := strings.Index(got, SegmentEnds), strings.Index(got, "ts=1700000012.000000"); end < 0 || end > after {
		t.Fatalf("the segment ends before a later message:\n%s", got)
	}
}

func TestContextReplyIsNumberedFromTheContext(t *testing.T) {
	in := dmInput()
	in.Messages[2].ThreadTS = "1700000001.000000"
	if l := lineOf(t, Render(in), "1700000012.000000"); !strings.Contains(l, "reply to c1") {
		t.Errorf("a reply to a context message = %q", l)
	}
}

func TestContextBefore(t *testing.T) {
	msgs := []slack.Message{
		{TS: "1.000000", Text: strings.Repeat("a", 50)},
		{TS: "2.000000", Text: strings.Repeat("b", 50)},
		{TS: "3.000000", Text: strings.Repeat("c", 50)},
		{TS: "4.000000", Text: strings.Repeat("d", 50)},
	}
	tss := func(ms []slack.Message) string {
		var out []string
		for _, m := range ms {
			out = append(out, m.TS)
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		n, chars int
		want     string
	}{
		{10, 1000, "1.000000,2.000000,3.000000,4.000000"},
		{2, 1000, "3.000000,4.000000"},
		{10, 120, "3.000000,4.000000"},
		{10, 10, ""},
		{0, 1000, ""},
	} {
		if got := tss(ContextBefore(msgs, c.n, c.chars)); got != c.want {
			t.Errorf("n=%d chars=%d: %q, want %q", c.n, c.chars, got, c.want)
		}
	}
}

func TestContextIsNeverAnExtractionTarget(t *testing.T) {
	in := dmInput()
	a := answerJSON(t, []obj{
		jItem("n1", "1700000001.000000"),
		jItem("n2", "1700000010.000000"),
	}, nil, []obj{
		{"item": "n2", "target": "github:acme/widget#1", "kind": "mention", "evidence_ts": "1700000002.000000"},
		{"item": "n2", "target": "github:acme/widget#2", "kind": "mention", "evidence_ts": "1700000011.000000"},
	}, nil)
	r, _, err := Extract(context.Background(), &scripted{answers: [][]byte{a}}, turns.Ask{}, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || r.Items[0].Ref != "n2" {
		t.Errorf("an item from the context was kept: %+v", r.Items)
	}
	if len(r.Links) != 1 || r.Links[0].EvidenceTS != "1700000011.000000" {
		t.Errorf("a link with context evidence was kept: %+v", r.Links)
	}
	if r.Dropped != 2 {
		t.Errorf("dropped %d, want the context item and its link counted", r.Dropped)
	}
	r, _, err = Extract(context.Background(), &scripted{answers: [][]byte{answerJSON(t, []obj{jItem("n1", "1700000099.000000"), jItem("n2", "1700000012.000000")}, nil, nil, nil)}}, turns.Ask{}, in)
	if err != nil || len(r.Items) != 1 || r.Items[0].Ref != "n2" || r.Dropped != 1 {
		t.Errorf("an origin no message has drops only its item: %+v %v", r, err)
	}
}

func TestTierFor(t *testing.T) {
	in := dmInput()
	size := len(Render(in))
	for _, c := range []struct {
		over int
		want turns.Tier
	}{{0, turns.TierFast}, {size, turns.TierFast}, {size - 1, turns.TierFrontier}} {
		if got := TierFor(in, turns.TierFast, c.over); got != c.want {
			t.Errorf("over %d (size %d): %s, want %s", c.over, size, got, c.want)
		}
	}
}
