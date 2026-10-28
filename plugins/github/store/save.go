package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

// Snapshot is one pull request as GitHub returned it, ready to store.
type Snapshot struct {
	PR     pr.PR      // Reasons are merged with the stored ones; LastSeenAt and Workspace are ignored
	Events []pr.Event // the timeline; those the host holds already are ignored
	Merges []pr.Event // merged events that fill a stored one lacking the merger or the commit
	Detail *Detail    // nil keeps the stored one
}

// UpsertPR stores p as SaveSnapshots does, seen at its LastSeenAt.
func (db *DB) UpsertPR(ctx context.Context, p pr.PR) error {
	_, err := db.SaveSnapshots(ctx, []Snapshot{{PR: p}}, p.LastSeenAt)
	return err
}

// StoredFacts is what storing a fresh GitHub snapshot needs of the stored one.
type StoredFacts struct {
	PR             pr.PR // zero when not Found
	Found          bool
	Pushes         map[string]time.Time // push key -> at
	UnfilledMerges []time.Time          // merged events lacking the merger or the commit
}

// StoredFacts reads the facts of refs: the stored pull requests, then
// their pushes and unfilled merges from the host's events.
func (db *DB) StoredFacts(ctx context.Context, refs []pr.Ref) (map[pr.Ref]StoredFacts, error) {
	prs, err := db.PRs(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[pr.Ref]StoredFacts, len(refs))
	for _, r := range refs {
		p, found := prs[r]
		out[r] = StoredFacts{PR: p, Found: found}
	}
	evs, err := db.HostEvents(ctx, refs, time.Time{}, pr.KindPush, pr.KindMerged)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		f := out[e.PR]
		switch {
		case e.Kind == pr.KindPush:
			if f.Pushes == nil {
				f.Pushes = map[string]time.Time{}
			}
			f.Pushes[e.Key] = e.At
		case e.Key == "" && (e.Actor == "" || e.Data == ""):
			f.UnfilledMerges = append(f.UnfilledMerges, e.At)
		}
		out[e.PR] = f
	}
	return out, nil
}

// stored is what saving a snapshot reads of the stored row.
type stored struct {
	found                  bool
	reasons                string
	mergeable, mergeableAt string
	tracked                bool
	ws                     string
	scope                  scope
	followedAt             time.Time // the newest followed event
	detail                 *storedDetail
}

type storedDetail struct {
	version      int
	readAt, body string
}

// SaveSnapshots stores snaps. The events, host data, go to the host
// first: the timeline events the host does not hold yet, the merges that
// fill a stored one, and a followed or unfollowed event for each pull
// request whose scope flips, derived from a read of the stored rows. Then
// one write transaction rewrites a row only when a column differs, records
// every ref as seen, and writes a detail only when it differs from the
// stored one. A write that fails after the host took the events leaves the
// rows as they were, so the next save derives the same events again and the
// host keeps each once. seenAt dates the sighting, a new conflict, the
// flips and a detail without ReadAt. It returns the events added per ref
// that gained any.
func (db *DB) SaveSnapshots(ctx context.Context, snaps []Snapshot, seenAt time.Time) (map[pr.Ref]int, error) {
	for _, snap := range snaps {
		if err := timed(snap.Events); err != nil {
			return nil, err
		}
	}
	if len(snaps) == 0 {
		return map[pr.Ref]int{}, nil
	}
	refs := make([]pr.Ref, len(snaps))
	kinds := map[string]bool{pr.KindFollowed: true, pr.KindUnfollowed: true}
	for i, snap := range snaps {
		refs[i] = snap.PR.Ref
		for _, e := range snap.Events {
			kinds[e.Kind] = true
		}
	}
	held, err := db.HostEvents(ctx, refs, time.Time{}, slices.Sorted(maps.Keys(kinds))...)
	if err != nil {
		return nil, err
	}
	known := map[eventID]bool{}
	flips := flipLog{}
	for _, e := range held {
		known[idOf(e)] = true
		flips.saw(e)
	}
	prior, err := storedRows(ctx, db.db, refs)
	if err != nil {
		return nil, err
	}
	added := map[pr.Ref]int{}
	var evs []pr.Event
	for _, snap := range snaps {
		ref := snap.PR.Ref
		was := prior[ref]
		was.followedAt = flips.followedAt[ref]
		w, err := db.advance(snap.PR, was, seenAt)
		if err != nil {
			return nil, err
		}
		prior[ref] = w.now
		for _, e := range snap.Events {
			if id := idOf(e); !known[id] {
				known[id] = true
				added[e.PR]++
				evs = append(evs, e)
			}
		}
		evs = append(evs, snap.Merges...)
		if w.flip != nil && flips.wants(*w.flip) {
			evs = append(evs, *w.flip)
			flips.saw(*w.flip)
		}
	}
	if err := db.RecordEvents(ctx, evs); err != nil {
		return nil, err
	}
	_, err = db.Write(ctx, func(tx *Tx) error {
		prior, err := storedRows(ctx, tx, refs)
		if err != nil {
			return err
		}
		upsert, err := tx.PrepareContext(ctx, upsertPRSQL)
		if err != nil {
			return err
		}
		defer upsert.Close()
		for _, snap := range snaps {
			ref := snap.PR.Ref
			now, err := db.savePR(ctx, upsert, tx, snap.PR, prior[ref], seenAt)
			if err != nil {
				return err
			}
			if snap.Detail != nil {
				d, err := saveDetail(ctx, tx, ref, *snap.Detail, now.detail, seenAt)
				if err != nil {
					return err
				}
				now.detail = &d
			}
			prior[ref] = now
		}
		return seeRefs(ctx, tx, refs, seenAt)
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// eventID is what makes an event one: its pull request, kind and key, or,
// unkeyed, its time.
type eventID struct {
	ref       pr.Ref
	kind, key string
}

func idOf(e pr.Event) eventID {
	key := e.Key
	if key == "" {
		key = "@" + formatTime(e.At)
	}
	return eventID{e.PR, e.Kind, key}
}

// rowWrite is what saving a snapshot over a stored row writes: the row as
// it then stands, its columns the store computes, and the followed or
// unfollowed event of a scope flip.
type rowWrite struct {
	now                    stored
	reasons, failing       string
	mergeable, mergeableAt string
	flip                   *pr.Event
}

// advance is what saving p over was writes, seen at seenAt.
func (db *DB) advance(p pr.PR, was stored, seenAt time.Time) (rowWrite, error) {
	w := rowWrite{now: was}
	w.mergeable, w.mergeableAt = nextMergeable(was.mergeable, was.mergeableAt, p.Mergeable, seenAt)
	storedReasons, err := splitList(was.reasons)
	if err != nil {
		return w, err
	}
	reasons := withTracked(append(slices.Clone(p.Reasons), storedReasons...), was.tracked)
	if len(p.FailingChecks) > 0 {
		raw, err := json.Marshal(p.FailingChecks)
		if err != nil {
			return w, err
		}
		w.failing = string(raw)
	}
	w.reasons = joinList(reasons)
	ended := p.MergedAt
	if ended.IsZero() {
		ended = p.ClosedAt
	}
	w.now.found, w.now.reasons, w.now.mergeable, w.now.mergeableAt = true, w.reasons, w.mergeable, w.mergeableAt
	if w.now.scope, err = scopeOf(reasons, was.ws, p.State, formatTime(ended)); err != nil {
		return w, err
	}
	if w.now.scope.followed == was.scope.followed {
		return w, nil
	}
	at := seenAt.UTC()
	if at.IsZero() {
		at = db.now().UTC()
	}
	e := flip(p.Ref, was.scope, w.now.scope, at, was.followedAt)
	if e.Kind == pr.KindFollowed && e.At.After(w.now.followedAt) {
		w.now.followedAt = e.At
	}
	w.flip = &e
	return w, nil
}

// savePR writes p over was unless no column differs, and returns the row
// as now stored.
func (db *DB) savePR(ctx context.Context, upsert *sql.Stmt, tx *Tx, p pr.PR, was stored, seenAt time.Time) (stored, error) {
	w, err := db.advance(p, was, seenAt)
	if err != nil {
		return was, err
	}
	res, err := upsert.ExecContext(ctx, p.Ref.Owner, p.Ref.Repo, p.Ref.Number, p.Title, p.Author, p.State, p.IsDraft, p.HeadOid, p.HeadBranch, p.BaseBranch,
		p.IsFork, p.ChecksState, p.ReviewDecision, joinList(p.ReviewRequests), p.URL, formatTime(p.CreatedAt), formatTime(p.UpdatedAt),
		formatTime(p.MergedAt), formatTime(p.ClosedAt), w.reasons, formatTime(p.BodyEditedAt), p.ChangedFiles, w.failing, w.mergeable, w.mergeableAt)
	if err != nil {
		return was, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return was, err
	}
	tx.Touch(p.Ref)
	return w.now, nil
}

// storedRows reads what saving needs of each of refs, absent ones included.
func storedRows(ctx context.Context, tx reader, refs []pr.Ref) (map[pr.Ref]stored, error) {
	out := make(map[pr.Ref]stored, len(refs))
	for chunk := range slices.Chunk(refs, prReadChunk) {
		marks, args := refValues(chunk)
		rows, err := tx.QueryContext(ctx, `WITH r(owner, repo, number) AS (VALUES `+marks+`)
			SELECT r.owner, r.repo, r.number, p.number IS NOT NULL, coalesce(p.reasons, ''), coalesce(p.mergeable, ''), coalesce(p.mergeable_at, ''),
			 coalesce(p.ws_id, ''), coalesce(p.state, ''), coalesce(iif(p.merged_at != '', p.merged_at, p.closed_at), ''), coalesce(o.tracked, 0),
			 d.number IS NOT NULL, coalesce(d.version, 0), coalesce(d.read_at, ''), coalesce(d.body, '')
			FROM r LEFT JOIN pull_requests p ON p.owner = r.owner AND p.repo = r.repo AND p.number = r.number
			LEFT JOIN overrides o ON o.unit_key = r.owner || '/' || r.repo || '#' || r.number
			LEFT JOIN pr_details d ON d.owner = r.owner AND d.repo = r.repo AND d.number = r.number`, args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref pr.Ref
				var st stored
				var state, ended string
				var hasDetail bool
				var d storedDetail
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &st.found, &st.reasons, &st.mergeable, &st.mergeableAt,
					&st.ws, &state, &ended, &st.tracked, &hasDetail, &d.version, &d.readAt, &d.body); err != nil {
					return err
				}
				if hasDetail {
					st.detail = &d
				}
				list, err := splitList(st.reasons)
				if err != nil {
					return err
				}
				if st.found {
					if st.scope, err = scopeOf(list, st.ws, state, ended); err != nil {
						return err
					}
				}
				out[ref] = st
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Stub is what a search returns of a pull request beyond its version: the
// facts the store refreshes without a full read.
type Stub struct {
	Ref            pr.Ref
	State          string
	Mergeable      string
	ReviewRequests []string
	ReviewDecision string
}

type stubRow struct{ requests, decision, mergeable, mergeableAt string }

// TouchStubs refreshes the review requests, the review decision, the
// sighting and the mergeable state of pull requests a search returned
// unchanged, in one transaction. A closed pull request's mergeable state is
// not followed.
func (db *DB) TouchStubs(ctx context.Context, stubs []Stub, seenAt time.Time) error {
	if len(stubs) == 0 {
		return nil
	}
	_, err := db.Write(ctx, func(tx *Tx) error {
		rows, err := stubRows(ctx, tx, stubs)
		if err != nil {
			return err
		}
		var sighted []pr.Ref
		for _, st := range stubs {
			row, ok := rows[st.Ref]
			if !ok {
				continue
			}
			next := row
			if st.State == "" || st.State == pr.StateOpen {
				next.mergeable, next.mergeableAt = nextMergeable(row.mergeable, row.mergeableAt, st.Mergeable, seenAt)
			}
			next.requests = joinList(st.ReviewRequests)
			next.decision = st.ReviewDecision
			sighted = append(sighted, st.Ref)
			if next == row {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE pull_requests SET review_requests=?, review_decision=?, mergeable=?, mergeable_at=? WHERE owner=? AND repo=? AND number=?`,
				next.requests, next.decision, next.mergeable, next.mergeableAt, st.Ref.Owner, st.Ref.Repo, st.Ref.Number); err != nil {
				return err
			}
			tx.Touch(st.Ref)
			rows[st.Ref] = next
		}
		return seeRefs(ctx, tx, sighted, seenAt)
	})
	return err
}

func stubRows(ctx context.Context, tx *Tx, stubs []Stub) (map[pr.Ref]stubRow, error) {
	out := make(map[pr.Ref]stubRow, len(stubs))
	for chunk := range slices.Chunk(stubs, prReadChunk) {
		refs := make([]pr.Ref, len(chunk))
		for i, st := range chunk {
			refs[i] = st.Ref
		}
		marks, args := refValues(refs)
		rows, err := tx.QueryContext(ctx, refsIn(`SELECT owner, repo, number, review_requests, review_decision, mergeable, mergeable_at FROM pull_requests
			WHERE (owner, repo, number)`, marks), args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var ref pr.Ref
				var r stubRow
				if err := rows.Scan(&ref.Owner, &ref.Repo, &ref.Number, &r.requests, &r.decision, &r.mergeable, &r.mergeableAt); err != nil {
					return err
				}
				out[ref] = r
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
