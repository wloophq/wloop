package storetest

import (
	"context"
	"crypto/sha1" //nolint:gosec // G505: derives fake commit ids for fixtures
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/event"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// layout is which pull request plays which part, a pure function of Sizes:
// pull request i is open when i < OpenPRs and linked when i < LinkedPRs.
type layout struct {
	refs         []pr.Ref
	turnPRs      []int // indexes into refs
	open, closed []pr.Ref
	withTurns    []unit.Unit
	handles      []string
}

func (s Sizes) check() error {
	switch {
	case s.OpenPRs > s.PRs, s.LinkedPRs > s.PRs, s.TurnPRs > s.PRs:
		return errors.New("more open, linked or turn pull requests than pull requests")
	case s.AgentLinkedPRs > s.LinkedPRs:
		return errors.New("more agent-linked pull requests than linked ones")
	case s.OpenSessions > s.Sessions:
		return errors.New("more open sessions than sessions")
	case s.Turns < s.TurnPRs, s.Turns > 0 && s.TurnPRs == 0:
		return errors.New("every pull request with a turn book needs a turn")
	case s.TurnPRs > 0 && s.Sessions == 0:
		return errors.New("turns need a session")
	}
	return nil
}

func layoutOf(s Sizes) layout {
	l := layout{}
	for i := range s.PRs {
		ref := pr.Ref{Owner: fmt.Sprintf("org-%d", i%3+1), Repo: fmt.Sprintf("repo-%d", i%8+1), Number: 1000 + i}
		l.refs = append(l.refs, ref)
		if i < s.OpenPRs {
			l.open = append(l.open, ref)
		} else {
			l.closed = append(l.closed, ref)
		}
	}
	for j := range s.TurnPRs {
		i := j * s.PRs / s.TurnPRs
		l.turnPRs = append(l.turnPRs, i)
		l.withTurns = append(l.withTurns, l.refs[i].Unit())
	}
	for k := range s.Sessions {
		l.handles = append(l.handles, fmt.Sprintf("term-%03d", k+1))
	}
	slices.SortFunc(l.open, compareRefs)
	slices.SortFunc(l.closed, compareRefs)
	slices.SortFunc(l.withTurns, func(a, b unit.Unit) int {
		switch {
		case a.Less(b):
			return -1
		case b.Less(a):
			return 1
		}
		return 0
	})
	return l
}

func compareRefs(a, b pr.Ref) int {
	switch {
	case a.Owner != b.Owner:
		return compareStrings(a.Owner, b.Owner)
	case a.Repo != b.Repo:
		return compareStrings(a.Repo, b.Repo)
	}
	return a.Number - b.Number
}

func compareStrings(a, b string) int {
	if a < b {
		return -1
	}
	return 1
}

// build seeds a store with s in a scratch directory and returns the file,
// whole once the close has checkpointed its write-ahead log.
func build(s Sizes) ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "storetest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "seed.db")
	if err := seedFile(path, s); err != nil {
		return nil, err
	}
	if wal, err := os.Stat(path + "-wal"); err == nil && wal.Size() > 0 {
		return nil, errors.New("the seeded store kept a write-ahead log after closing")
	}
	return os.ReadFile(path)
}

func seedFile(path string, s Sizes) error {
	f, err := open(path)
	if err != nil {
		return err
	}
	err = seed(context.Background(), f, s, layoutOf(s))
	return errors.Join(err, f.DB.Close())
}

func seed(ctx context.Context, f *Fixture, s Sizes, l layout) error {
	for _, step := range []func(context.Context, *Fixture, Sizes, layout) error{
		seedMeta, seedPRs, seedWorkspaces, seedSessions, seedTurns, seedBriefRuns, seedEvents,
	} {
		if err := step(ctx, f, s, l); err != nil {
			return err
		}
	}
	return nil
}

func oid(parts ...int) string {
	sum := sha1.Sum([]byte(fmt.Sprint(parts))) //nolint:gosec // G401: derives fake commit ids for fixtures
	return hex.EncodeToString(sum[:])
}

func ago(d time.Duration) time.Time { return seedNow.Add(-d) }

// seedMeta replaces the wall-clock stamp the store takes at creation.
func seedMeta(ctx context.Context, f *Fixture, _ Sizes, _ layout) error {
	return f.Inventory.SetMeta(ctx, "timings_since", ago(eventWindow*time.Minute).Format(event.TimeLayout))
}

// lifeOf is when pull request i was created and when it ended: merged or
// closed for a closed one, seedNow for an open one.
func lifeOf(s Sizes, i int) (created, end time.Time) {
	created = ago(time.Duration(30+i%60) * 24 * time.Hour)
	if i < s.OpenPRs {
		return created, seedNow
	}
	return created, created.Add(time.Duration(1+i%9) * 24 * time.Hour)
}

// within is a time strictly inside pull request i's life, a minute clear of
// both ends, at fraction num/den of it.
func within(s Sizes, i, num, den int) time.Time {
	created, end := lifeOf(s, i)
	span := end.Sub(created) - 2*time.Minute
	return created.Add(time.Minute + time.Duration(int64(span)/int64(den)*int64(num)))
}

func merged(s Sizes, i int) bool { return i >= s.OpenPRs && i%4 != 0 }

func seedPRs(ctx context.Context, f *Fixture, s Sizes, l layout) error {
	var lifecycle []dinv.Event
	for i, ref := range l.refs {
		created, end := lifeOf(s, i)
		author := fmt.Sprintf("user-%02d", i%17+1)
		p := dinv.PR{
			Ref:            ref,
			Title:          fmt.Sprintf("Change %d", i+1),
			Author:         author,
			State:          dgh.StateOpen,
			IsDraft:        i < s.OpenPRs && i%11 == 0,
			HeadOid:        oid(i),
			HeadBranch:     fmt.Sprintf("change-%d", i+1),
			BaseBranch:     "main",
			ChecksState:    []string{"SUCCESS", "FAILURE", "PENDING", ""}[i%4],
			ReviewDecision: []string{"REVIEW_REQUIRED", "APPROVED", "CHANGES_REQUESTED", ""}[i%4],
			URL:            ref.URL(),
			CreatedAt:      created,
			UpdatedAt:      within(s, i, 9, 10),
			ChangedFiles:   1 + i%40,
			LastSeenAt:     ago(time.Duration(i) * time.Minute),
			Reasons:        []string{[]string{"author", "review-requested", "assignee"}[i%3]},
			Mergeable:      "MERGEABLE",
		}
		if i%3 == 1 {
			p.ReviewRequests = []string{fmt.Sprintf("user-%02d", i%17+2)}
		}
		lifecycle = append(lifecycle, dinv.Event{PR: ref, Kind: dinv.KindOpened, Actor: author, At: created})
		switch {
		case merged(s, i):
			p.State, p.MergedAt, p.ClosedAt = dgh.StateMerged, end, end
			lifecycle = append(lifecycle, dinv.Event{PR: ref, Kind: dinv.KindMerged, Actor: fmt.Sprintf("user-%02d", (i+5)%17+1), At: end, Data: oid(i, -1)})
		case i >= s.OpenPRs:
			p.State, p.ClosedAt = dgh.StateClosed, end
			lifecycle = append(lifecycle, dinv.Event{PR: ref, Kind: dinv.KindClosed, Actor: author, At: end})
		}
		if err := f.Inventory.UpsertPR(ctx, p); err != nil {
			return fmt.Errorf("pull request %s: %w", ref, err)
		}
	}
	return appendAll(ctx, f, lifecycle)
}

func appendAll(ctx context.Context, f *Fixture, evs []dinv.Event) error {
	added, err := f.Inventory.AppendEvents(ctx, evs)
	if err != nil {
		return err
	}
	if added != len(evs) {
		return fmt.Errorf("%d of %d seeded events collided with stored ones", len(evs)-added, len(evs))
	}
	return nil
}

func seedWorkspaces(ctx context.Context, f *Fixture, s Sizes, l layout) error {
	for i := range s.LinkedPRs {
		ws := dinv.Workspace{
			ID:          fmt.Sprintf("ws-%03d", i+1),
			DisplayName: fmt.Sprintf("change-%d", i+1),
			Status:      []string{"in-progress", "in-review", "done"}[i%3],
			IsReview:    i%3 == 1,
		}
		if i < s.AgentLinkedPRs {
			ws.AgentStates = []turn.AgentState{[]turn.AgentState{turn.AgentWorking, turn.AgentWaiting, turn.AgentDone}[i%3]}
		}
		if err := f.Inventory.SetWorkspace(ctx, l.refs[i], ws); err != nil {
			return fmt.Errorf("workspace of %s: %w", l.refs[i], err)
		}
	}
	return nil
}

// sessionEvent is an idle or active transition of a session, written with no
// unit and keyed by the idle start, as the turns service writes it.
func sessionEvent(kind, handle string, since, at time.Time) port.Event {
	return port.Event{Kind: kind, Key: handle + ":" + since.Format(time.RFC3339Nano), At: at}
}

// seedSessions gives the sessions their share of idle/active pairs, plus an
// unpaired idle for each open session still idle.
func seedSessions(ctx context.Context, f *Fixture, s Sizes, l layout) error {
	pairs := int(math.Round(EventMix[turns.KindSessionActive] * float64(s.Events)))
	for k, h := range l.handles {
		started := ago(time.Duration(k+1) * 3 * time.Hour)
		ses := turn.Session{
			ID:           h,
			Pane:         fmt.Sprintf("pane-%03d", k+1),
			AgentSession: fmt.Sprintf("agent-%03d", k+1),
			WorkspaceID:  fmt.Sprintf("ws-%03d", k%max(s.LinkedPRs, 1)+1),
			Worktree:     fmt.Sprintf("/work/tree-%03d", k+1),
			Home:         fmt.Sprintf("/work/home-%03d", k%7+1),
			Managed:      k%2 == 0,
			StartedAt:    started,
			MCPAt:        started.Add(2 * time.Second),
		}
		var evs []port.Event
		for m := 0; k+m*len(l.handles) < pairs; m++ {
			since := started.Add(time.Duration(m+1) * 20 * time.Minute)
			evs = append(evs, sessionEvent(turns.KindSessionIdle, h, since, since), sessionEvent(turns.KindSessionActive, h, since, since.Add(5*time.Minute)))
		}
		switch {
		case k >= s.OpenSessions:
			ses.ClosedAt = started.Add(2 * time.Hour)
		case k%4 == 0:
			ses.IdleSince = started.Add(110 * time.Minute)
			evs = append(evs, sessionEvent(turns.KindSessionIdle, h, ses.IdleSince, ses.IdleSince))
		}
		if err := f.Turns.SaveSession(ctx, ses, evs...); err != nil {
			return fmt.Errorf("session %s: %w", h, err)
		}
	}
	return nil
}

// tailStates is the state the newest turn of book j ends in, by j modulo its
// length; an open state on a closed pull request, or replaced on a book of
// one turn, falls back to done.
var tailStates = []turn.State{turn.Started, turn.Sent, turn.Asked, turn.Failed, turn.Unrecorded,
	turn.Dismissed, turn.Replaced, turn.Withdrawn, turn.Settled, turn.Done}

func seedTurns(ctx context.Context, f *Fixture, s Sizes, l layout) error {
	n := 0
	for j, i := range l.turnPRs {
		ref := l.refs[i]
		count := s.Turns / s.TurnPRs
		if j < s.Turns%s.TurnPRs {
			count++
		}
		tail := tailStates[j%len(tailStates)]
		if (tail.Open() && i >= s.OpenPRs) || (tail == turn.Replaced && count < 2) {
			tail = turn.Done
		}
		b := turn.Book{Unit: ref.Unit()}
		for k := range count {
			want := turn.Done
			switch {
			case k == count-1 && tail == turn.Replaced:
				want = turn.Sent
			case k == count-2 && tail == turn.Replaced:
				want = turn.Replaced
			case k == count-1:
				want = tail
			}
			handle := l.handles[n%len(l.handles)]
			sent := within(s, i, k+1, count+1)
			t := turn.Turn{
				Intent:       turn.Intent([]string{"review", "address"}[j%2]),
				Role:         []string{"round.review", "round.address"}[j%2],
				ID:           fmt.Sprintf("round-%03d", n+1),
				Trigger:      []turn.Trigger{turn.Automated, turn.ByHand}[n%2],
				Head:         oid(i, k),
				SentTo:       handle,
				Session:      handle,
				State:        turn.Started,
				SentAt:       sent,
				StartedAt:    sent.Add(10 * time.Second),
				Agent:        "claude",
				AgentSession: fmt.Sprintf("agent-%03d", n%len(l.handles)+1),
			}
			if want == turn.Sent {
				t.State, t.Session, t.StartedAt = turn.Sent, "", time.Time{}
			}
			b.Turns = append(b.Turns, t)
			saved, err := f.Turns.Save(ctx, b)
			if err != nil {
				return fmt.Errorf("turns of %s: %w", ref, err)
			}
			b = saved
			if b, err = endTurn(ctx, f, b, want, n); err != nil {
				return fmt.Errorf("turns of %s: %w", ref, err)
			}
			n++
		}
	}
	return nil
}

// endTurn moves the book's newest turn to want: by a record and its
// turn_ended event for a state a record sets, directly otherwise.
func endTurn(ctx context.Context, f *Fixture, b turn.Book, want turn.State, n int) (turn.Book, error) {
	t := &b.Turns[len(b.Turns)-1]
	at := t.SentAt.Add(20 * time.Minute)
	var rec record.Record
	switch want {
	case turn.Sent, turn.Started:
		return b, nil
	case turn.Replaced:
		t.State, t.Before, t.EndedAt = turn.Replaced, turn.Started, at
		return f.Turns.Save(ctx, b)
	case turn.Dismissed, turn.Withdrawn:
		t.State, t.EndedAt = want, at
		return f.Turns.Save(ctx, b)
	case turn.Unrecorded:
		rec = record.Unrecorded("")
	case turn.Asked:
		rec = record.Record{State: record.Asked, Summary: fmt.Sprintf("Question %d", n+1), Question: &record.Question{
			Kind: "choice", Text: "Which way?", Options: []record.Option{
				{ID: "a", Label: "First way", Consequence: "keeps the change"},
				{ID: "b", Label: "Second way", Consequence: "drops the change"},
			}}}
	default:
		rec = record.Record{State: record.State(want), Summary: fmt.Sprintf("Summary %d", n+1)}
	}
	hash := b.RecordHash(t.Seq, rec)
	b.Record(turn.Record{TurnSeq: t.Seq, Session: t.Session, AgentSession: t.AgentSession, Head: t.Head, Hash: hash, At: at, Acked: true, Record: rec})
	if want == turn.Asked {
		b.Answer(turn.Answer{TurnSeq: t.Seq, Text: "First way (option a)", OptionID: "a", Source: turn.Cockpit, Session: t.Session, At: at.Add(time.Minute)})
	}
	ended := port.Event{Unit: b.Unit, Kind: turns.KindTurnEnded, Key: t.ID + ":" + hash[:12], Data: rec.EventData(), At: at}
	return f.Turns.Save(ctx, b, ended)
}

func seedBriefRuns(ctx context.Context, f *Fixture, s Sizes, l layout) error {
	if s.BriefRuns > 0 && s.PRs == 0 {
		return errors.New("brief runs need a pull request")
	}
	for k := range s.BriefRuns {
		i := k % s.PRs
		started := within(s, i, 1+k/s.PRs, 2+s.BriefRuns/s.PRs)
		r := dinv.BriefRun{
			PR:           l.refs[i],
			Trigger:      []string{"background", "wanted", "refresh"}[k%3],
			Version:      "v1",
			AttemptOf:    oid(i),
			HeadOid:      oid(i),
			ChangedFiles: 1 + i%40,
			StartedAt:    started,
		}
		id, err := f.Inventory.StartBriefRun(ctx, r)
		if err != nil {
			return fmt.Errorf("brief run %d: %w", k, err)
		}
		in, call, cost := int64(40+k%30), int64(9000+k%5000), 0.01+float64(k%20)/100
		r.ID, r.EndedAt, r.InputMS, r.CallMS, r.CostUSD, r.Model = id, started.Add(40*time.Second), &in, &call, &cost, "model-a"
		st := dinv.BriefRecord{AttemptOf: r.AttemptOf}
		if k%9 == 0 {
			r.Outcome, r.Error, st.Failures, st.RetryAt = "failed", "agent exited 1", 1, r.EndedAt.Add(time.Hour)
		} else {
			r.Outcome, r.Brief = "ok", fmt.Sprintf(`{"summary":"Brief %d"}`, k+1)
			r.Files = []string{fmt.Sprintf("pkg/file%d.go", i%13)}
			r.Tags = []string{fmt.Sprintf("area:part-%d", i%6), fmt.Sprintf("kind:%s", []string{"fix", "feature", "chore"}[i%3])}
		}
		if err := f.Inventory.EndBriefRun(ctx, r, st); err != nil {
			return fmt.Errorf("brief run %d: %w", k, err)
		}
	}
	return nil
}

// derived are the kinds the earlier steps write in the number their rows
// call for, which the events step does not top up to EventMix.
var derived = []string{dinv.KindOpened, dinv.KindMerged, dinv.KindClosed, dinv.KindBriefEnded,
	turns.KindSessionIdle, turns.KindSessionActive}

// eventCounts is how many events each kind needs to reach s.Events in
// EventMix's proportions, given the ones the earlier steps already wrote.
func eventCounts(s Sizes, present map[string]int) (map[string]int, error) {
	need := map[string]int{}
	total := 0
	for _, n := range present {
		total += n
	}
	for kind, share := range EventMix {
		if kind == dinv.KindReview || slices.Contains(derived, kind) {
			continue
		}
		if n := int(math.Round(share*float64(s.Events))) - present[kind]; n > 0 {
			need[kind] = n
			total += n
		}
	}
	need[dinv.KindReview] += s.Events - total
	if need[dinv.KindReview] < 0 {
		return nil, fmt.Errorf("the other steps wrote %d events more than %d", -need[dinv.KindReview], s.Events)
	}
	return need, nil
}

func seedEvents(ctx context.Context, f *Fixture, s Sizes, l layout) error {
	present, err := kindCounts(ctx, f)
	if err != nil {
		return err
	}
	need, err := eventCounts(s, present)
	if err != nil {
		return err
	}
	kinds := make([]string, 0, len(need))
	for k := range need {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	var evs []dinv.Event
	situations := map[pr.Ref]int{}
	for ki, kind := range kinds {
		for idx := range need[kind] {
			evs = append(evs, makeEvent(s, l, kind, ki, idx, situations))
		}
	}
	return appendAll(ctx, f, evs)
}

func kindCounts(ctx context.Context, f *Fixture) (map[string]int, error) {
	rows, err := f.DB.R().QueryContext(ctx, `SELECT kind, count(*) FROM events GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		out[kind] = n
	}
	return out, rows.Err()
}

// eventWindow is how far back the store's timings start.
const eventWindow = 90 * 24 * 60

// makeEvent is event idx of kind on a pull request, inside its life; the idx
// milliseconds keep two unkeyed events of a kind on one pull request apart.
func makeEvent(s Sizes, l layout, kind string, ki, idx int, situations map[pr.Ref]int) dinv.Event {
	i := (idx*7 + ki) % s.PRs
	ref := l.refs[i]
	created, end := lifeOf(s, i)
	span := int(end.Sub(created)/time.Minute) - 2
	at := created.Add(time.Duration(1+(idx*7919+ki*131)%span)*time.Minute + 13*time.Second + time.Duration(idx)*time.Millisecond)
	e := dinv.Event{PR: ref, Kind: kind, At: at, Key: strconv.Itoa(100000 + idx)}
	user := fmt.Sprintf("user-%02d", (idx+ki)%17+1)
	switch kind {
	case dinv.KindReady, dinv.KindDraft:
		e.Key, e.Actor = "", user
	case dinv.KindPush:
		e.Key = oid(i, idx, ki)
	case dinv.KindReview:
		e.Actor, e.Data = user, []string{"APPROVED", "COMMENTED", "CHANGES_REQUESTED"}[idx%3]+":"+oid(i)
	case dinv.KindComment:
		e.Actor = user
	case dinv.KindCheck:
		e.Key = oid(i, idx) + ":" + []string{"SUCCESS", "FAILURE", "PENDING"}[idx%3]
	case dinv.KindReviewRequest, dinv.KindReviewRequestRemoved:
		e.Key, e.Data, e.Actor = user+":"+at.Format(time.RFC3339), user, fmt.Sprintf("user-%02d", i%17+1)
	case dinv.KindSituation:
		situations[ref]++
		e.Key, e.Data = strconv.Itoa(situations[ref]), []string{"review:me", "address:me", "wait:author", "wait:reviewers"}[idx%4]
	case dinv.KindFollowed, dinv.KindUnfollowed:
		e.Key, e.Data = at.Format(event.TimeLayout), "author"
	case dinv.KindStatus:
		e.Data = []string{"in-progress", "in-review", "done"}[idx%3]
	}
	return e
}
