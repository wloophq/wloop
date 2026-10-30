package slack

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
)

const (
	findDM   = "D0000000009"
	findLink = "https://example.slack.com/archives/C0000000001/p1700000000000200"
)

func findSlack(t *testing.T) (*fixture, *[]url.Values) {
	t.Helper()
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	var queries []url.Values
	f.api.Handle("conversations.info", func(v url.Values) any {
		return map[string]any{"channel": map[string]any{"id": v.Get("channel"), "name": "releases", "is_channel": true, "is_member": true}}
	})
	f.api.Handle("users.lookupByEmail", func(v url.Values) any {
		return map[string]any{"user": map[string]any{"id": other, "name": "dana"}}
	})
	f.api.Handle("search.messages", func(v url.Values) any {
		queries = append(queries, v)
		return map[string]any{"messages": map[string]any{"total": 42, "paging": map[string]any{"page": 1, "pages": 3}, "matches": []map[string]any{
			{"ts": "1700000000.000200", "user": other, "text": "release 1.2 is out\nchangelog below", "permalink": findLink,
				"channel": map[string]any{"id": readConv, "name": "releases"}},
			{"ts": "1700000100.000100", "user": other, "text": "between us", "permalink": "https://example.slack.com/archives/" + findDM + "/p1700000100000100",
				"channel": map[string]any{"id": findDM, "is_im": true}},
			{"ts": "1700000150.000100", "user": other, "text": "also between us", "permalink": "https://example.slack.com/archives/" + findDM + "/p1700000150000100",
				"channel": map[string]any{"id": findDM}},
			{"ts": "1700000200.000100", "username": "deploybot", "text": "", "permalink": "https://example.slack.com/archives/C0000000001/p1700000200000100",
				"channel": map[string]any{"id": readConv, "name": "releases"}},
			{"ts": "1700000250.000100", "user": other, "text": "no link here",
				"channel": map[string]any{"id": readConv, "name": "releases"}},
		}}}
	})
	return f, &queries
}

func TestSlackSearchRendersOneLinePerMessage(t *testing.T) {
	f, queries := findSlack(t)
	r := f.reader()
	r.Location = time.FixedZone("-03", -3*60*60)
	got, err := r.Search(context.Background(), SearchQuery{Query: "release", Channel: "#releases", From: "@dana", After: "2023-11-01", Before: "2023-11-30", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	q := (*queries)[0]
	if q.Get("query") != "release in:#releases from:@dana after:2023-11-01 before:2023-11-30" || q.Get("count") != "10" || q.Get("sort") != "timestamp" {
		t.Fatalf("query %v", q)
	}
	want := "3 of 42 matches for release in:#releases from:@dana after:2023-11-01 before:2023-11-30\n" +
		findLink + " · n (U0000000002) · 2023-11-14 19:13 -03 · release 1.2 is out\n" +
		"https://example.slack.com/archives/C0000000001/p1700000200000100 · deploybot (bot) · 2023-11-14 19:16 -03 · (no text)\n" +
		"no permalink (C0000000001/1700000250.000100) · n (U0000000002) · 2023-11-14 19:17 -03 · no link here\n" +
		"(2 direct messages that are not tracked left out)"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSlackSearchResolvesChannelAndAuthorIDs(t *testing.T) {
	f, queries := findSlack(t)
	r := f.reader()
	for _, c := range []struct {
		q    SearchQuery
		want string
	}{
		{SearchQuery{Query: "x", Channel: readConv, From: other}, "x in:#releases from:<@U0000000002>"},
		{SearchQuery{Query: "x", From: "dana@example.com"}, "x from:<@U0000000002>"},
		{SearchQuery{Query: "x", From: "@" + other}, "x from:<@U0000000002>"},
	} {
		if _, err := r.Search(context.Background(), c.q); err != nil {
			t.Fatal(err)
		}
		if got := (*queries)[len(*queries)-1].Get("query"); got != c.want {
			t.Errorf("%+v: query %q, want %q", c.q, got, c.want)
		}
	}
	for _, c := range []string{"general", findDM} {
		if _, err := r.Search(context.Background(), SearchQuery{Query: "x", Channel: c}); err == nil || !strings.Contains(err.Error(), "#channel") {
			t.Fatalf("channel %q: %v", c, err)
		}
	}
}

func TestSlackSearchShowsATrackedDirectMessage(t *testing.T) {
	f, _ := findSlack(t)
	if _, err := (Segments{Store: f.db.Segments(), Clock: fixedClock(f.now)}).Apply(context.Background(), team, findDM,
		[]dslack.Span{{FromTS: "1700000000.000000"}}); err != nil {
		t.Fatal(err)
	}
	got, err := f.reader().Search(context.Background(), SearchQuery{Query: "us"})
	if err != nil || !strings.Contains(got, "also between us") || strings.Contains(got, "left out") {
		t.Fatalf("%v\n%s", err, got)
	}
}
