package slack

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	slackapi "github.com/wloophq/wloop/internal/slack"
)

// CursorBackfill is set once the first start's backfill finished.
const CursorBackfill = "backfill"

// DefaultSince is how far back the backfill reads without slack.ingest.since.
const DefaultSince = 7 * 24 * time.Hour

const (
	// drainEvery is how often the backfill looks whether the inbox saved its deltas.
	drainEvery       = 100 * time.Millisecond
	backfillMaxPages = 20
	searchPageSize   = 100
)

func (pl *poller) backfilled(ctx context.Context) (bool, error) {
	v, err := pl.p.src.db.Cursor(ctx, CursorBackfill)
	return v != "", err
}

// since is the backfill's start: slack.ingest.since, else DefaultSince ago.
func (pl *poller) since(now time.Time) time.Time {
	if s := pl.p.cfg.Ingest.Since; s != "" {
		if t, err := time.Parse(time.DateOnly, s); err == nil {
			return t
		}
		pl.p.logf("slack backfill: since %q is not YYYY-MM-DD; reading %s back", s, DefaultSince)
	}
	return now.Add(-DefaultSince)
}

type threadKey struct{ conv, anchor string }

// backfill reads the DMs and group DMs, the viewer's messages and the
// mentions of the viewer since slack.ingest.since, with every thread they
// sit in, through the socket's admission; then sets CursorBackfill.
func (pl *poller) backfill(ctx context.Context) error {
	src := pl.p.src
	team, me := src.viewer()
	if me == "" {
		return errors.New("slack backfill: viewer unknown")
	}
	now := pl.p.now()
	since := pl.since(now)
	oldest := strconv.FormatInt(since.Unix(), 10) + ".000000"
	pl.p.slack.Metrics.Backfill(0, 0, 0, false)
	convs, err := pl.dms(ctx, team)
	if err != nil {
		return err
	}
	t := &tally{objects: map[string]bool{}}
	threads := map[threadKey]dslack.ConversationKind{}
	read := 0
	for _, c := range convs {
		kind := dslack.ConversationKind(c.Kind)
		msgs, err := pl.history(ctx, c.ID, oldest)
		if gone(err) {
			pl.p.logf("slack backfill: conversation %s: %v", c.ID, err)
			continue
		}
		if err != nil {
			return err
		}
		read++
		if err := pl.deliver(ctx, c.ID, kind, msgs, t); err != nil {
			return err
		}
		pl.p.slack.Metrics.Backfill(read, 0, t.deltas, false)
		if read%100 == 0 {
			pl.p.logf("slack backfill: %d of %d conversations read, %d deltas", read, len(convs), t.deltas)
		}
		for _, m := range msgs {
			if m.Anchor() == m.TS && m.ReplyCount > 0 && dslack.CompareTS(m.LatestReply, oldest) > 0 {
				threads[threadKey{c.ID, m.TS}] = kind
			}
		}
	}
	after := since.AddDate(0, 0, -1).Format(time.DateOnly)
	for _, q := range []string{"from:<@" + me + "> after:" + after, "<@" + me + "> after:" + after} {
		if err := pl.searchThreads(ctx, q, oldest, threads); err != nil {
			return err
		}
	}
	refs := make([]threadKey, 0, len(threads))
	for r := range threads {
		refs = append(refs, r)
	}
	slices.SortFunc(refs, func(a, b threadKey) int {
		return cmp.Or(cmp.Compare(a.conv, b.conv), dslack.CompareTS(a.anchor, b.anchor))
	})
	for i, r := range refs {
		pl.p.slack.Metrics.Backfill(read, i, t.deltas, false)
		msgs, err := src.replies(ctx, r.conv, r.anchor)
		if gone(err) {
			pl.p.logf("slack backfill: thread %s/%s: %v", r.conv, r.anchor, err)
			continue
		}
		if err != nil {
			return err
		}
		if err := pl.deliver(ctx, r.conv, threads[r], msgs, t); err != nil {
			return err
		}
	}
	pl.p.slack.Metrics.Backfill(read, len(refs), t.deltas, false)
	if err := pl.drained(ctx, t.objects); err != nil {
		return err
	}
	if err := src.db.SetCursor(ctx, CursorBackfill, now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	pl.p.slack.Metrics.Backfill(read, len(refs), t.deltas, true)
	return nil
}

// dms lists the viewer's DMs and group DMs and stores their kinds.
func (pl *poller) dms(ctx context.Context, team string) ([]sqlite.ConversationRow, error) {
	var out []sqlite.ConversationRow
	cursor := ""
	for range backfillMaxPages {
		page, err := pl.p.src.client.ListConversations(ctx, "im,mpim", cursor)
		if err != nil {
			return nil, err
		}
		for _, c := range page.Conversations {
			out = append(out, conversationRow(team, c))
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, pl.p.src.db.PutConversations(ctx, out)
}

type searchMatch struct {
	TS        string `json:"ts"`
	ThreadTS  string `json:"thread_ts"`
	Permalink string `json:"permalink"`
	Channel   struct {
		ID        string `json:"id"`
		IsIM      bool   `json:"is_im"`
		IsMPIM    bool   `json:"is_mpim"`
		IsGroup   bool   `json:"is_group"`
		IsPrivate bool   `json:"is_private"`
	} `json:"channel"`
}

// searchThreads adds the thread of every match of q at or after oldest;
// matches come newest first, so paging stops at the first older one.
func (pl *poller) searchThreads(ctx context.Context, q, oldest string, threads map[threadKey]dslack.ConversationKind) error {
	for page := 1; page <= backfillMaxPages; page++ {
		res, err := pl.p.src.client.SearchMessages(ctx, slackapi.SearchArgs{Query: q, Sort: "timestamp", SortDir: "desc",
			Page: page, Count: searchPageSize})
		if err != nil {
			return err
		}
		for _, raw := range res.Matches {
			var m searchMatch
			if err := json.Unmarshal(raw, &m); err != nil {
				return err
			}
			if dslack.CompareTS(m.TS, oldest) < 0 {
				return nil
			}
			if m.Channel.ID == "" || m.TS == "" {
				continue
			}
			anchor := m.ThreadTS
			if anchor == "" && m.Permalink != "" {
				if _, _, threadTS, err := parsePermalink(m.Permalink); err == nil {
					anchor = threadTS
				}
			}
			kind := infoKind(m.Channel.IsIM, m.Channel.IsMPIM, m.Channel.IsPrivate || m.Channel.IsGroup)
			threads[threadKey{m.Channel.ID, cmp.Or(anchor, m.TS)}] = kind
		}
		if page >= res.Pages {
			return nil
		}
	}
	return nil
}

// drained waits until the inbox saved or gave up on every object; a Touch
// it gave up on leaves a floor for the next catch-up.
func (pl *poller) drained(ctx context.Context, objects map[string]bool) error {
	list := make([]string, 0, len(objects))
	for o := range objects {
		list = append(list, o)
	}
	tick := time.NewTicker(drainEvery)
	defer tick.Stop()
	for len(pl.p.src.inbox.peek(list)) > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}
