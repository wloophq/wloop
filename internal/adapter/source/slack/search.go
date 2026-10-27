package slack

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	slackapi "github.com/wloophq/wloop/internal/slack"
)

// SearchEvery is how often Discover runs the mention search; a catch-up runs it whatever its age.
const SearchEvery = 5 * time.Minute

// SkipSearchLimited is why a search did not run: search.messages is inside
// its Retry-After window or spent its budget for the minute.
const SkipSearchLimited = "slack: search.messages budget is low"

const (
	searchMethod   = "search.messages"
	searchCount    = 100
	searchMaxPages = 5
	// searchOverlap re-reads the minutes before the cursor, which the search
	// index may not have held yet when the cursor was taken.
	searchOverlap = 2 * time.Minute
	defaultSince  = 7 * 24 * time.Hour
)

// searcher runs search.messages for the viewer's mentions and each
// configured term, newest first, until the cursor. It never admits: it
// returns the matches the store does not hold yet as deltas.
type searcher struct {
	client  *slackapi.Client
	db      *sqlite.SlackStore
	viewer  func() (team, user string)
	terms   []string
	since   string
	now     func() time.Time
	log     func(format string, args ...any)
	metrics *Metrics

	mu   sync.Mutex
	last time.Time

	errMu  sync.Mutex
	failed error
}

func newSearcher(client *slackapi.Client, db *sqlite.SlackStore, terms []string, since string, viewer func() (string, string),
	now func() time.Time, metrics *Metrics, log func(string, ...any)) *searcher {
	s := &searcher{client: client, db: db, viewer: viewer, since: since, now: now, metrics: metrics, log: log}
	if metrics != nil {
		metrics.Warn("search", s.warnings)
	}
	for _, t := range terms {
		if t = strings.TrimSpace(t); t != "" {
			s.terms = append(s.terms, t)
		}
	}
	return s
}

// searchRun is one search's outcome; skip names why it did not run.
type searchRun struct {
	deltas []dslack.Delta
	next   string
	skip   string
}

// run searches from cursor unless due says the last search is recent;
// next is the newest ts seen, "" when nothing moved the cursor.
func (s *searcher) run(ctx context.Context, cursor string, due bool) (searchRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if due && !s.last.IsZero() && now.Sub(s.last) < SearchEvery {
		return searchRun{skip: "searched recently"}, nil
	}
	_, me := s.viewer()
	if me == "" {
		return searchRun{skip: "viewer unknown"}, nil
	}
	if s.client.Limits != nil && s.client.Limits.Low(searchMethod) {
		s.logf("slack search skipped: %s", SkipSearchLimited)
		return searchRun{skip: SkipSearchLimited}, nil
	}
	if tsTime(cursor).IsZero() {
		cursor = ""
	}
	stop, after := s.window(cursor, now)
	queries := append([]string{"<@" + me + ">"}, s.terms...)
	out := searchRun{}
	seen := map[string]bool{}
	newest := cursor
	for _, q := range queries {
		ds, top, err := s.query(ctx, phrase(q)+" after:"+after, stop, now)
		if err != nil {
			if ctx.Err() == nil {
				s.last = now
				s.setFailed(err)
			}
			return searchRun{}, err
		}
		if dslack.CompareTS(top, newest) > 0 {
			newest = top
		}
		for _, d := range ds {
			if k := d.Conversation + "/" + d.TS; !seen[k] {
				seen[k] = true
				out.deltas = append(out.deltas, d)
			}
		}
	}
	s.last = now
	s.setFailed(nil)
	if newest != cursor {
		out.next = newest
	}
	return out, nil
}

func (s *searcher) setFailed(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	s.failed = err
}

// warnings name the last search's error while searches keep failing.
func (s *searcher) warnings() []string {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.failed == nil {
		return nil
	}
	return []string{fmt.Sprintf("slack: the mention search fails: %v; mentions outside your channels are not found", s.failed)}
}

// window is the ts paging stops at and the after: date. after: excludes
// its day and reads in the viewer's time zone, so it starts two days early.
func (s *searcher) window(cursor string, now time.Time) (stop, after string) {
	from := now.Add(-defaultSince)
	if d, err := time.Parse(time.DateOnly, s.since); err == nil {
		from = d
	}
	if cursor != "" {
		from = tsTime(cursor).Add(-searchOverlap)
		stop = fmt.Sprintf("%d.000000", from.Unix())
	}
	return stop, from.AddDate(0, 0, -2).UTC().Format(time.DateOnly)
}

// query pages one query until a match at or before stop; top is the newest
// match's ts.
func (s *searcher) query(ctx context.Context, query, stop string, now time.Time) (out []dslack.Delta, top string, err error) {
	for page := 1; page <= searchMaxPages; page++ {
		p, err := s.client.SearchMessages(ctx, slackapi.SearchArgs{Query: query, Sort: "timestamp", SortDir: "desc", Page: page, Count: searchCount})
		if err != nil {
			return nil, "", err
		}
		for _, raw := range p.Matches {
			d, ok, err := searchDelta(raw, now)
			if err != nil {
				return nil, "", err
			}
			if !ok {
				continue
			}
			if stop != "" && dslack.CompareTS(d.TS, stop) <= 0 {
				return out, top, nil
			}
			if dslack.CompareTS(d.TS, top) > 0 {
				top = d.TS
			}
			if _, stored, err := storedMessage(ctx, s.db, d.Conversation, d.TS); err != nil {
				return nil, "", err
			} else if !stored {
				out = append(out, d)
			}
		}
		if len(p.Matches) == 0 || page >= p.Pages {
			return out, top, nil
		}
	}
	s.logf("slack search: %d pages read before the cursor", searchMaxPages)
	return out, top, nil
}

// phrase quotes a term that holds a space.
func phrase(q string) string {
	if strings.ContainsAny(q, " \t") && !strings.HasPrefix(q, `"`) {
		return `"` + q + `"`
	}
	return q
}

// searchDelta is a match as a new-message delta with Via search; a match
// with no channel or ts is skipped. The thread comes from the permalink.
func searchDelta(raw json.RawMessage, now time.Time) (dslack.Delta, bool, error) {
	var w searchMatch
	if err := json.Unmarshal(raw, &w); err != nil {
		return dslack.Delta{}, false, fmt.Errorf("slack search match: %w", err)
	}
	if w.Channel.ID == "" || w.TS == "" {
		return dslack.Delta{}, false, nil
	}
	m, err := dslack.MessageFrom(raw)
	if err != nil {
		return dslack.Delta{}, false, err
	}
	if m.ThreadTS == "" && w.Permalink != "" {
		if _, _, threadTS, err := parseMessage(w.Permalink); err == nil {
			m.ThreadTS = threadTS
		}
	}
	if m.ThreadTS == m.TS {
		m.ThreadTS = ""
	}
	return dslack.Delta{Conversation: w.Channel.ID, ChannelType: w.channelType(), TS: m.TS, ThreadTS: m.ThreadTS,
		Kind: dslack.KindMessage, Message: &m, Via: dslack.ViaSearch, ReceivedAt: now}, true, nil
}

func (w searchMatch) channelType() string {
	switch {
	case w.Channel.IsIM:
		return "im"
	case w.Channel.IsMPIM:
		return "mpim"
	case w.Channel.IsGroup || w.Channel.IsPrivate:
		return "group"
	}
	return "channel"
}

func (s *searcher) logf(format string, args ...any) {
	if s.log != nil {
		s.log(format, args...)
	}
}

// Discover runs the mention search every SearchEvery and returns the
// objects of the matches it admits; the next cursor is the newest match's
// ts, saved with the pass's facts. A failed search is logged and the pass
// goes on with the watched objects.
func (s *Source) Discover(ctx context.Context, cursor string) ([]port.Stub, string, error) {
	if s.search == nil {
		return nil, "", nil
	}
	run, err := s.search.run(ctx, cursor, true)
	if err != nil && ctx.Err() == nil {
		s.logf("slack search: %v", err)
		return nil, "", nil
	}
	if err != nil || run.skip != "" {
		return nil, "", err
	}
	team, _ := s.viewer()
	var stubs []port.Stub
	via := map[string]map[string]dslack.Via{}
	kinds := map[string]dslack.ConversationKind{}
	for _, d := range run.deltas {
		v, err := s.admit.admit(ctx, d)
		if err != nil {
			return nil, "", err
		}
		if v.rule == "" {
			continue
		}
		s.search.metrics.Admitted(d.Via)
		kinds[d.Conversation] = cmp.Or(kinds[d.Conversation], v.kind)
		i := slices.IndexFunc(stubs, func(st port.Stub) bool { return st.Object == v.object })
		if i < 0 {
			i = len(stubs)
			stubs = append(stubs, port.Stub{Object: v.object})
			via[v.object] = map[string]dslack.Via{}
		}
		if dslack.CompareTS(d.TS, stubs[i].Version) > 0 {
			stubs[i].Version = d.TS
		}
		via[v.object][d.Conversation+"/"+d.TS] = d.Via
	}
	convs, err := s.newConversations(ctx, kinds)
	if err != nil {
		return nil, "", err
	}
	for i := range stubs {
		f := facts{Facts: dslack.Facts{Team: team}, via: via[stubs[i].Object]}
		if i == 0 {
			f.Conversations = convs
		}
		stubs[i].Light = f
	}
	return stubs, run.next, nil
}

// search is the catch-up's mention search: it runs whatever the last
// search's age, sends each match through admission into the inbox and
// returns how many it admitted. A failed search is logged and warned in
// health, never failing the catch-up. It keeps the cursor: the inbox is memory, so
// the next Discover reads these matches again from the cursor saved with
// stored facts, skipping those already stored.
func (p *plugin) search(ctx context.Context, reason string) (int, error) {
	if p.src == nil || p.src.search == nil {
		return 0, nil
	}
	cursor, err := p.src.db.Cursor(ctx, sqlite.SlackDiscoverCursor)
	if err != nil {
		return 0, err
	}
	run, err := p.src.search.run(ctx, cursor, false)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		p.logf("slack search for %s: %v", reason, err)
		return 0, nil
	}
	n := 0
	for _, d := range run.deltas {
		v, err := p.src.admit.admit(ctx, d)
		if err != nil {
			return n, fmt.Errorf("slack search for %s: %w", reason, err)
		}
		if v.rule == "" {
			continue
		}
		n++
		p.slack.Metrics.Admitted(d.Via)
		p.src.inbox.add(v.object, v.kind, d)
	}
	return n, nil
}
