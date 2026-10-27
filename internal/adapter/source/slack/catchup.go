package slack

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	slackapi "github.com/wloophq/wloop/internal/slack"
)

const (
	// StartOverlap is how far before the high-water mark the start's
	// catch-up reads: a stop drops envelopes acked but still queued and
	// deltas still in the inbox, while later messages moved the mark.
	StartOverlap = 10 * time.Minute
	// DMSearch is the search.messages modifier for direct and group messages.
	DMSearch = "is:dm"
	// DMThreadWindow is how far before a DM's high-water mark the catch-up
	// probes stored threads for replies: history carries no replies.
	DMThreadWindow = 7 * 24 * time.Hour
	// catchUpMaxPages bounds one conversation's history in a catch-up.
	catchUpMaxPages = 20
)

// everWatched is past every next check, so Watched lists every watched object.
var everWatched = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// tally counts what a catch-up admitted that the store and inbox lacked.
type tally struct {
	deltas, missed int
	countMissed    bool
	// objects are the objects whose deltas went to the inbox.
	objects map[string]bool
	// read are the threads whose replies were read, by "<conv>/<anchor>".
	read map[string]bool
}

// catchUp reads what the socket may have lost: conversations.history from
// the high-water mark of each admitted conversation, conversations.replies
// of each watched thread whose parent moved, and the mention search. Every
// message goes through the socket's admission and inbox.
func (pl *poller) catchUp(ctx context.Context, reason string) (err error) {
	floors := pl.takeFloors()
	defer func() {
		if err != nil {
			pl.restoreFloors(floors)
		}
	}()
	db := pl.p.src.db
	admitted, err := db.AdmittedConversations(ctx)
	if err != nil {
		return err
	}
	watched, err := db.Watched(ctx, everWatched)
	if err != nil {
		return err
	}
	rows := map[string]sqlite.ConversationRow{}
	for _, c := range admitted {
		rows[c.ID] = c
	}
	threads := map[string]map[string]string{}
	var others []string
	for _, o := range watched {
		if o.TS == "" || o.Kind == sqlite.SlackDM || o.Version == "" {
			continue
		}
		if threads[o.Conversation] == nil {
			threads[o.Conversation] = map[string]string{}
			if _, ok := rows[o.Conversation]; !ok {
				others = append(others, o.Conversation)
			}
		}
		threads[o.Conversation][o.TS] = o.Version
	}
	for conv := range floors {
		if _, ok := rows[conv]; !ok && threads[conv] == nil {
			others = append(others, conv)
		}
	}
	if len(others) > 0 {
		known, err := db.Conversations(ctx, others)
		if err != nil {
			return err
		}
		for _, c := range known {
			rows[c.ID] = c
		}
		for _, id := range others {
			if _, ok := rows[id]; !ok {
				rows[id] = sqlite.ConversationRow{ID: id}
			}
		}
	}
	convs := make([]string, 0, len(rows))
	for id := range rows {
		convs = append(convs, id)
	}
	slices.Sort(convs)
	named := make([]string, 0, len(threads))
	for id := range threads {
		named = append(named, id)
	}
	if err := pl.p.src.nameConversations(ctx, named); err != nil {
		return err
	}
	t := &tally{read: map[string]bool{}, countMissed: reason == ReasonReconnect || reason == ReasonClockJump || reason == ReasonDown}
	for _, conv := range convs {
		oldest := ""
		if c := rows[conv]; c.Admitted {
			oldest = c.HWM
			if reason == ReasonStart && oldest != "" {
				oldest = tsMinus(oldest, StartOverlap)
			}
		}
		if f, ok := floors[conv]; ok {
			if before, ok := precedingTS(f); ok && (oldest == "" || dslack.CompareTS(before, oldest) < 0) {
				oldest = before
			}
		}
		if err := pl.catchUpConversation(ctx, rows[conv], oldest, threads[conv], t); err != nil {
			return err
		}
	}
	if reason != ReasonDropped {
		if err := pl.catchUpDMs(ctx, rows, t); err != nil {
			return err
		}
	}
	if pl.Search != nil {
		n, err := pl.Search(ctx, reason)
		if err != nil {
			return err
		}
		t.deltas += n
	}
	pl.p.slack.Metrics.CatchUp(reason, t.deltas, t.missed)
	return nil
}

// catchUpConversation reads conv's history after oldest ("" reads none) and
// the replies of its watched threads that moved, and of its DM threads with
// replies after oldest.
func (pl *poller) catchUpConversation(ctx context.Context, c sqlite.ConversationRow, oldest string, watched map[string]string, t *tally) error {
	kind := dslack.ConversationKind(c.Kind)
	if kind == "" {
		var err error
		if kind, err = pl.p.src.admit.kindOf(ctx, dslack.Delta{Conversation: c.ID}); err != nil {
			return err
		}
	}
	if kind.DM() && c.Admitted && c.HWM != "" {
		stored, err := pl.dmThreads(ctx, c.ID, tsMinus(c.HWM, DMThreadWindow))
		if err != nil {
			return err
		}
		for a, v := range watched {
			stored[a] = v
		}
		watched = stored
	}
	var msgs []dslack.Message
	if oldest != "" {
		var err error
		msgs, err = pl.history(ctx, c.ID, oldest)
		if gone(err) {
			pl.p.logf("slack catch-up: conversation %s: %v", c.ID, err)
			return nil
		}
		if err != nil {
			return err
		}
		if err := pl.deliver(ctx, c.ID, kind, msgs, t); err != nil {
			return err
		}
	}
	after := map[string]string{}
	rest := map[string]string{}
	for anchor, v := range watched {
		rest[anchor] = v
	}
	for _, m := range msgs {
		if m.Anchor() != m.TS {
			continue
		}
		if v, ok := watched[m.TS]; ok {
			delete(rest, m.TS)
			if threadVersion(m) != v {
				after[m.TS] = versionTS(v)
			}
			continue
		}
		if kind.DM() && m.ReplyCount > 0 && dslack.CompareTS(m.LatestReply, oldest) > 0 {
			after[m.TS] = oldest
		}
	}
	if len(rest) > 0 {
		got, err := pl.probe(ctx, c.ID, rest)
		if err != nil {
			return err
		}
		for anchor, v := range got {
			if v != rest[anchor] {
				after[anchor] = versionTS(rest[anchor])
			}
		}
	}
	anchors := make([]string, 0, len(after))
	for a := range after {
		anchors = append(anchors, a)
	}
	slices.SortFunc(anchors, dslack.CompareTS)
	for _, a := range anchors {
		if t.read != nil {
			t.read[c.ID+"/"+a] = true
		}
		replies, err := pl.p.src.replies(ctx, c.ID, a)
		if gone(err) {
			pl.p.logf("slack catch-up: thread %s/%s: %v", c.ID, a, err)
			continue
		}
		if err != nil {
			return err
		}
		replies = slices.DeleteFunc(replies, func(m dslack.Message) bool {
			return m.TS != a && dslack.CompareTS(m.TS, after[a]) <= 0
		})
		if err := pl.deliver(ctx, c.ID, kind, replies, t); err != nil {
			return err
		}
	}
	return nil
}

// dmThreads are the stored versions of the DM's threads with a reply,
// anchored at or after from.
func (pl *poller) dmThreads(ctx context.Context, conv, from string) (map[string]string, error) {
	rows, err := pl.p.src.db.Messages(ctx, conv, sqlite.MessageQuery{From: from})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range rows {
		if r.ThreadTS != "" && r.ThreadTS != r.TS && !r.Deleted && dslack.CompareTS(r.TS, out[r.ThreadTS]) > 0 {
			out[r.ThreadTS] = r.TS
		}
	}
	return out, nil
}

// catchUpDMs reads the DMs and group DMs the store has no admitted object
// in: a DM search from the latest high-water mark, then their history.
func (pl *poller) catchUpDMs(ctx context.Context, rows map[string]sqlite.ConversationRow, t *tally) error {
	start := ""
	for _, c := range rows {
		if c.Admitted && dslack.CompareTS(c.HWM, start) > 0 {
			start = c.HWM
		}
	}
	if start == "" {
		return nil
	}
	start = tsMinus(start, StartOverlap)
	q := DMSearch + " after:" + tsTime(start).AddDate(0, 0, -1).Format(time.DateOnly)
	found := map[string]dslack.ConversationKind{}
	var convs []string
	replied := map[threadKey]dslack.ConversationKind{}
	for page := 1; page <= catchUpMaxPages; page++ {
		res, err := pl.p.src.client.SearchMessages(ctx, slackapi.SearchArgs{Query: q, Sort: "timestamp", SortDir: "desc",
			Page: page, Count: searchPageSize})
		if err != nil && ctx.Err() == nil {
			pl.dmSearch.fail(err, pl.p)
			return nil
		}
		if err != nil {
			return err
		}
		pl.dmSearch.fail(nil, pl.p)
		older := false
		for _, raw := range res.Matches {
			var m searchMatch
			if err := json.Unmarshal(raw, &m); err != nil {
				return err
			}
			if dslack.CompareTS(m.TS, start) <= 0 {
				older = true
				break
			}
			id := m.Channel.ID
			if !(m.Channel.IsIM || m.Channel.IsMPIM) {
				pl.dmSearchOnce.Do(func() {
					pl.p.logf("slack catch-up: search ignored %s: a match in %s is no DM", DMSearch, id)
				})
				continue
			}
			if id == "" {
				continue
			}
			if c := rows[id]; c.Admitted {
				anchor := m.ThreadTS
				if anchor == "" && m.Permalink != "" {
					if _, _, threadTS, err := parseMessage(m.Permalink); err == nil {
						anchor = threadTS
					}
				}
				if anchor != "" && anchor != m.TS && dslack.CompareTS(anchor, c.HWM) <= 0 && !t.read[id+"/"+anchor] {
					replied[threadKey{id, anchor}] = infoKind(m.Channel.IsIM, m.Channel.IsMPIM, false)
				}
				continue
			}
			if _, ok := found[id]; !ok {
				found[id] = infoKind(m.Channel.IsIM, m.Channel.IsMPIM, false)
				convs = append(convs, id)
			}
		}
		if older || page >= res.Pages {
			break
		}
	}
	slices.Sort(convs)
	for _, id := range convs {
		if err := pl.catchUpConversation(ctx, sqlite.ConversationRow{ID: id, Kind: string(found[id])}, start, nil, t); err != nil {
			return err
		}
	}
	keys := make([]threadKey, 0, len(replied))
	for k := range replied {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b threadKey) int {
		return cmp.Or(cmp.Compare(a.conv, b.conv), dslack.CompareTS(a.anchor, b.anchor))
	})
	for _, k := range keys {
		msgs, err := pl.p.src.replies(ctx, k.conv, k.anchor)
		if gone(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := pl.deliver(ctx, k.conv, replied[k], msgs, t); err != nil {
			return err
		}
	}
	return nil
}

// history is conv's messages after oldest, ascending ts.
func (pl *poller) history(ctx context.Context, conv, oldest string) ([]dslack.Message, error) {
	var out []dslack.Message
	cursor := ""
	for i := range catchUpMaxPages {
		page, err := pl.p.src.client.History(ctx, conv, slackapi.PageArgs{Oldest: oldest, Cursor: cursor, Limit: readPage})
		if err != nil {
			return nil, err
		}
		if out, err = appendPage(out, page.Messages); err != nil {
			return nil, err
		}
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		if i == catchUpMaxPages-1 {
			pl.p.logf("slack catch-up: conversation %s: stopped after %d pages", conv, catchUpMaxPages)
		}
		cursor = page.NextCursor
	}
	slices.SortFunc(out, func(a, b dslack.Message) int { return dslack.CompareTS(a.TS, b.TS) })
	return out, nil
}

// deliver admits each message the store and inbox lack, or hold with other
// content, as a catch-up delta, as the socket would.
func (pl *poller) deliver(ctx context.Context, conv string, kind dslack.ConversationKind, msgs []dslack.Message, t *tally) error {
	src := pl.p.src
	for _, m := range msgs {
		if slices.Contains(dslack.HiddenSubtypes, m.Subtype) {
			continue
		}
		known, same, err := pl.known(ctx, conv, m)
		if err != nil {
			return err
		}
		if same {
			continue
		}
		msg := m
		d := dslack.Delta{Conversation: conv, ChannelType: channelType(kind), TS: m.TS, ThreadTS: m.ThreadTS,
			Kind: dslack.KindMessage, Message: &msg, Via: dslack.ViaCatchUp, ReceivedAt: pl.p.now()}
		v, err := src.admit.admit(ctx, d)
		if err != nil {
			return err
		}
		if v.rule == "" {
			continue
		}
		pl.p.slack.Metrics.Admitted(d.Via)
		src.inbox.add(v.object, v.kind, d)
		if t.objects != nil {
			t.objects[v.object] = true
		}
		if known {
			continue
		}
		t.deltas++
		if t.countMissed && pl.gaps.connectedAt(tsTime(m.TS)) {
			t.missed++
		}
	}
	return nil
}

// known says whether the inbox or the store holds m, and whether with the
// same content.
func (pl *poller) known(ctx context.Context, conv string, m dslack.Message) (known, same bool, _ error) {
	held, ok := pl.p.src.inbox.message(conv, m.TS)
	if !ok {
		var err error
		if held, ok, err = storedMessage(ctx, pl.p.src.db, conv, m.TS); err != nil {
			return false, false, err
		}
	}
	if !ok {
		return false, false, nil
	}
	return true, sameMessage(held, m), nil
}

func sameMessage(held, read dslack.Message) bool {
	if held.Text != read.Text || held.EditedTS != read.EditedTS || held.Deleted != read.Deleted {
		return false
	}
	return read.Reactions == nil || held.Reactions == nil || sameReactions(held.Reactions, read.Reactions)
}

func sameReactions(a, b []dslack.Reaction) bool {
	set := func(rs []dslack.Reaction) []string {
		var out []string
		for _, r := range rs {
			for _, u := range r.Users {
				out = append(out, r.Name+" "+u)
			}
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(set(a), set(b))
}

// conversationKind is conv's stored kind; an unknown G conversation, a
// private channel or a legacy group DM, costs one conversations.info.
func (p *plugin) conversationKind(ctx context.Context, conv string) (dslack.ConversationKind, error) {
	if strings.HasPrefix(conv, "G") && p.slack.Client != nil {
		rows, err := p.src.db.Conversations(ctx, []string{conv})
		if err != nil {
			return "", err
		}
		if len(rows) == 0 || rows[0].Kind == "" {
			info, err := p.slack.Client.ConversationInfo(ctx, conv)
			if err != nil {
				return "", err
			}
			team, _ := p.src.viewer()
			row := conversationRow(team, info)
			if err := p.src.db.PutConversations(ctx, []sqlite.ConversationRow{row}); err != nil {
				return "", err
			}
			return dslack.ConversationKind(row.Kind), nil
		}
	}
	return p.src.admit.kindOf(ctx, dslack.Delta{Conversation: conv})
}

func conversationRow(team string, c slackapi.ConversationInfo) sqlite.ConversationRow {
	return sqlite.ConversationRow{ID: c.ID, Team: team, Name: c.Name, Kind: string(infoKind(c.IsIM, c.IsMPIM, c.IsPrivate || c.IsGroup)),
		IsMember: c.IsMember || c.IsIM || c.IsMPIM, Archived: c.IsArchived, DMUser: c.User}
}

func infoKind(im, mpim, private bool) dslack.ConversationKind {
	switch {
	case im:
		return dslack.KindIM
	case mpim:
		return dslack.KindMPIM
	case private:
		return dslack.KindPrivate
	}
	return dslack.KindChannel
}

func channelType(k dslack.ConversationKind) string {
	switch k {
	case dslack.KindChannel:
		return "channel"
	case dslack.KindPrivate:
		return "group"
	case dslack.KindIM:
		return "im"
	case dslack.KindMPIM:
		return "mpim"
	}
	return ""
}

// versionTS is the ts a stored thread version names: a lone edited
// message's version is "<ts>+<edited>".
func versionTS(v string) string {
	ts, _, _ := strings.Cut(v, "+")
	return ts
}

// tsMinus is ts moved d earlier.
func tsMinus(ts string, d time.Duration) string {
	sec, frac, _ := strings.Cut(ts, ".")
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return ts
	}
	out := strconv.FormatInt(n-int64(d/time.Second), 10)
	if frac != "" {
		out += "." + frac
	}
	return out
}

// searchFailure is the last error of the catch-up's DM search: logged when
// it changes and warned in health until a search succeeds.
type searchFailure struct {
	mu   sync.Mutex
	err  string
	once sync.Once
}

func (f *searchFailure) fail(err error, p *plugin) {
	f.once.Do(func() { p.slack.Metrics.Warn("dm-search", f.warnings) })
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	f.mu.Lock()
	changed := msg != f.err
	f.err = msg
	f.mu.Unlock()
	if changed && msg != "" {
		p.logf("slack catch-up: the DM search fails, DMs of conversations with nothing admitted wait for the next catch-up: %s", msg)
	}
}

func (f *searchFailure) warnings() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == "" {
		return nil
	}
	return []string{"slack: the DM search of the catch-up fails: " + f.err + "; new DMs arrive only through the socket"}
}
