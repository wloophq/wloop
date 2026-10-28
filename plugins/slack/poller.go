package slack

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/ingest"
	slackapi "github.com/wloophq/wloop/plugins/slack/slackapi"
	"github.com/wloophq/wloop/plugins/slack/socket"
)

// SkipBudget prefixes the job's skip text while a probe method's budget is spent.
const SkipBudget = "slack: budget low for "

const (
	// DropEvery spaces the catch-ups that recover what the inbox dropped.
	DropEvery = 5 * time.Minute
	// retryFirst and retryMax bound the backoff of a failed catch-up.
	retryFirst = time.Minute
	retryMax   = 30 * time.Minute
)

var probeMethods = []string{"conversations.history", "conversations.replies"}

// poller runs the first start's backfill and the catch-ups the gap detector
// asks for, and probes watched threads for the ingestion pass.
type poller struct {
	p    *plugin
	gaps *gaps
	// Search runs a catch-up's mention search and returns the deltas it
	// admitted; nil skips it.
	Search func(ctx context.Context, reason string) (int, error)
	tick   time.Duration

	fallback     atomic.Bool
	fallbackOnce sync.Once
	dmSearchOnce sync.Once
	dmSearch     searchFailure

	mu          sync.Mutex
	fails       map[string]int
	floors      map[string]string
	dropPending bool
	lastDrop    time.Time
	failed      string
	retryAt     time.Time
	backoff     time.Duration
}

func newPoller(p *plugin) *poller {
	if c := p.slack.Client; c != nil && c.Limits == nil {
		c.Limits = &slackapi.Limiter{}
	}
	return &poller{p: p, gaps: newGaps(), tick: GapTick, fails: map[string]int{}, floors: map[string]string{}}
}

// polled is the Source whose Stubs probe watched threads at Slack.
type polled struct {
	*Source
	poll *poller
}

func (s polled) Stubs(ctx context.Context, objects []string) ([]ingest.Stub, error) {
	return s.poll.stubs(ctx, objects)
}

func (p *plugin) onState(s socket.State) {
	p.slack.Metrics.Socket(s)
	if p.poll != nil {
		p.poll.gaps.state(s)
	}
}

// budget is the skip text of a pass while a probe method is rate limited.
func (pl *poller) budget() string {
	l := pl.p.slack.Client.Limits
	if l == nil {
		return ""
	}
	for _, m := range probeMethods {
		if l.Low(m) {
			return SkipBudget + m
		}
	}
	return ""
}

// run catches up on start (backfilling a first start) and on every gap,
// until ctx ends.
func (pl *poller) run(ctx context.Context) {
	pl.gaps.reset()
	pl.gaps.mark(ReasonStart)
	t := time.NewTicker(pl.tick)
	defer t.Stop()
	for {
		if r := pl.gaps.take(); r != "" {
			pl.runOnce(ctx, r)
			if ctx.Err() != nil {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-pl.gaps.wake:
		case <-t.C:
			pl.gaps.tick()
			pl.due()
		}
	}
}

func (pl *poller) runOnce(ctx context.Context, reason string) {
	err := pl.catchUpOrBackfill(ctx, reason)
	if ctx.Err() != nil {
		return
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if reason == ReasonDropped {
		pl.lastDrop = pl.gaps.wall()
	}
	if err == nil {
		pl.backoff = 0
		return
	}
	pl.backoff = min(max(2*pl.backoff, retryFirst), retryMax)
	pl.failed, pl.retryAt = reason, pl.gaps.wall().Add(pl.backoff)
	pl.p.logf("slack catch-up (%s): %v; retrying in %s", reason, err, pl.backoff)
}

func (pl *poller) catchUpOrBackfill(ctx context.Context, reason string) error {
	if reason == ReasonStart {
		done, err := pl.backfilled(ctx)
		if err != nil {
			return err
		}
		if !done {
			return pl.backfill(ctx)
		}
	}
	return pl.catchUp(ctx, reason)
}

// due marks a failed catch-up whose backoff ended, and the recovery of what
// the inbox dropped once DropEvery passed since the last one.
func (pl *poller) due() {
	now := pl.gaps.wall()
	pl.mu.Lock()
	var marks []string
	if pl.failed != "" && !now.Before(pl.retryAt) {
		marks = append(marks, pl.failed)
		pl.failed = ""
	}
	if pl.dropPending && (pl.lastDrop.IsZero() || now.Sub(pl.lastDrop) >= DropEvery) {
		marks = append(marks, ReasonDropped)
		pl.dropPending = false
	}
	pl.mu.Unlock()
	for _, r := range marks {
		pl.gaps.mark(r)
	}
}

// touched counts failed Touches per object as the inbox does; when the
// inbox gives up on an object, its deltas' conversations get a floor the
// next catch-up reads history from.
func (pl *poller) touched(objects []string, err error) {
	if pl == nil {
		return
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	for _, o := range objects {
		if err == nil {
			delete(pl.fails, o)
			continue
		}
		pl.fails[o]++
		if pl.fails[o] < touchAttempts {
			continue
		}
		delete(pl.fails, o)
		for _, e := range pl.p.src.inbox.peek([]string{o})[o] {
			conv, ts := e.delta.Conversation, e.delta.TS
			if f, ok := pl.floors[conv]; !ok || dslack.CompareTS(ts, f) < 0 {
				pl.floors[conv] = ts
			}
			pl.dropPending = true
		}
	}
}

func (pl *poller) takeFloors() map[string]string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	out := pl.floors
	pl.floors = map[string]string{}
	pl.dropPending = false
	return out
}

func (pl *poller) restoreFloors(fs map[string]string) {
	if len(fs) == 0 {
		return
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	for conv, ts := range fs {
		if f, ok := pl.floors[conv]; !ok || dslack.CompareTS(ts, f) < 0 {
			pl.floors[conv] = ts
		}
	}
	pl.dropPending = true
}

// stubs are the due objects' versions: a DM or a never-read object at its
// stored version, a thread at the version its parent shows at Slack.
func (pl *poller) stubs(ctx context.Context, objects []string) ([]ingest.Stub, error) {
	vs, err := pl.p.src.db.Versions(ctx, objects)
	if err != nil {
		return nil, err
	}
	out := make([]ingest.Stub, len(objects))
	at := map[string]int{}
	byConv := map[string]map[string]string{}
	var convs []string
	for i, o := range objects {
		out[i] = ingest.Stub{Object: o, Version: vs[o]}
		conv, ts, err := dslack.ParseObject(o)
		if err != nil {
			return nil, err
		}
		if ts == "" || vs[o] == "" {
			continue
		}
		if byConv[conv] == nil {
			byConv[conv] = map[string]string{}
			convs = append(convs, conv)
		}
		byConv[conv][ts] = vs[o]
		at[o] = i
	}
	slices.Sort(convs)
	for _, conv := range convs {
		got, err := pl.probe(ctx, conv, byConv[conv])
		if err != nil {
			return nil, err
		}
		for anchor, v := range got {
			out[at[dslack.ThreadObject(conv, anchor)]].Version = v
		}
	}
	return out, nil
}

// probe gives the current version of conv's threads, keyed by anchor, from
// their stored versions: one conversations.history over the window holding
// the parents, conversations.replies limit=1 for a lone thread, a parent
// the window missed, or a token whose history lacks latest_reply. A thread
// Slack no longer serves is left out.
func (pl *poller) probe(ctx context.Context, conv string, stored map[string]string) (map[string]string, error) {
	anchors := make([]string, 0, len(stored))
	for a := range stored {
		anchors = append(anchors, a)
	}
	slices.SortFunc(anchors, dslack.CompareTS)
	found := map[string]string{}
	if len(anchors) > 1 && !pl.fallback.Load() {
		parents, err := pl.parents(ctx, conv, anchors[0], anchors[len(anchors)-1])
		if gone(err) {
			pl.p.logf("slack probe: conversation %s: %v", conv, err)
			return found, nil
		}
		if err != nil {
			return nil, err
		}
		for _, a := range anchors {
			m, ok := parents[a]
			if !ok {
				continue
			}
			if lacksLatestReply(m, stored[a]) {
				pl.useFallback(conv)
				clear(found)
				break
			}
			found[a] = threadVersion(m)
		}
	}
	for _, a := range anchors {
		if _, ok := found[a]; ok {
			continue
		}
		page, err := pl.p.src.client.Replies(ctx, conv, a, slackapi.PageArgs{Limit: 1})
		if gone(err) {
			pl.p.logf("slack probe: thread %s/%s: %v", conv, a, err)
			continue
		}
		if err != nil {
			return nil, err
		}
		msgs, err := appendPage(nil, page.Messages)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			if m.TS == a {
				found[a] = threadVersion(m)
			}
		}
	}
	return found, nil
}

func (pl *poller) useFallback(conv string) {
	pl.fallback.Store(true)
	pl.fallbackOnce.Do(func() {
		pl.p.logf("slack probe: conversations.history in %s carries no latest_reply; probing threads one by one with conversations.replies", conv)
	})
}

// parents are the thread parents conversations.history holds from oldest
// to latest, both included, by ts.
func (pl *poller) parents(ctx context.Context, conv, oldest, latest string) (map[string]dslack.Message, error) {
	out := map[string]dslack.Message{}
	cursor := ""
	for range readMaxPages {
		page, err := pl.p.src.client.History(ctx, conv, slackapi.PageArgs{Oldest: oldest, Latest: latest, Inclusive: true,
			Cursor: cursor, Limit: readPage})
		if err != nil {
			return nil, err
		}
		msgs, err := appendPage(nil, page.Messages)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			if m.Anchor() == m.TS {
				out[m.TS] = m
			}
		}
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

func threadVersion(parent dslack.Message) string {
	return dslack.Thread{TS: parent.TS, LatestReply: parent.LatestReply, Messages: []dslack.Message{parent}}.Version()
}

// lacksLatestReply says whether a history parent shows replies, or had
// them stored, without saying which is the latest.
func lacksLatestReply(parent dslack.Message, stored string) bool {
	if parent.LatestReply != "" {
		return false
	}
	return parent.ReplyCount > 0 || (stored != parent.TS && !strings.HasPrefix(stored, parent.TS+"+"))
}
