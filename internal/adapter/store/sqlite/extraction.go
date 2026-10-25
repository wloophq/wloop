package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ExtractionStore holds what the Slack extraction job keeps per unit: its cursor and last
// answer, the hints of each item, the day's spend and a log of its calls.
type ExtractionStore struct{ db *DB }

func (db *DB) Extraction() *ExtractionStore { return &ExtractionStore{db} }

// ExtractionState is one unit's row. Watched is written only by SetWatched;
// ClosureMarked, a sent draft's unit.closed, never by Save. Brief is the goal
// of the unit's brief in use, which Save keeps as the unit's extraction brief
// run of the day.
type ExtractionState struct {
	LastTS                                        string
	LastSeq                                       int64
	Brief, Verdict, VerdictWhy, ViewerRole, Topic string
	StartedByViewer, ClosurePosted, Watched       bool
	ClosureMarked                                 bool
	At                                            time.Time
}

type ExtractionHint struct{ Mode, Context, Why string }

type ExtractionRun struct {
	Unit    unit.Unit
	At      time.Time
	Model   string
	CostUSD *float64
	Outcome string
	Error   string
}

// ExtractionSave is one run's result, written in one transaction.
type ExtractionSave struct {
	States map[unit.Unit]ExtractionState
	// Hints replace the hints of the items they name.
	Hints map[unit.Unit]map[string]ExtractionHint
}

const (
	extractionStateColumns = `extraction_cursors.provider, extraction_cursors.unit_key, last_ts, last_seq, COALESCE(json_extract(r.brief, '$.goal'), ''),
		verdict, verdict_why, viewer_role, topic, started_by_viewer, closure_posted, watched, closure_marked, extraction_cursors.at`
	extractionFrom = ` FROM extraction_cursors LEFT JOIN unit_briefs b ON b.provider = extraction_cursors.provider AND b.unit_key = extraction_cursors.unit_key
		LEFT JOIN brief_runs r ON b.run != 0 AND r.id = b.run`
	extractionStatesSQL = `SELECT ` + extractionStateColumns + extractionFrom + `
		WHERE (extraction_cursors.provider, extraction_cursors.unit_key) IN (SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]') FROM json_each(?))`
	extractionWatchedSQL  = `SELECT ` + extractionStateColumns + extractionFrom + ` WHERE extraction_cursors.provider = ? AND watched = 1`
	putExtractionStateSQL = `INSERT INTO extraction_cursors (provider, unit_key, last_ts, last_seq, verdict, verdict_why,
		viewer_role, topic, started_by_viewer, closure_posted, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, unit_key) DO UPDATE SET last_ts = excluded.last_ts, last_seq = excluded.last_seq,
		verdict = excluded.verdict, verdict_why = excluded.verdict_why,
		viewer_role = excluded.viewer_role, topic = excluded.topic, started_by_viewer = excluded.started_by_viewer,
		closure_posted = excluded.closure_posted, at = excluded.at`
	// extractionBriefRunSQL is the unit's extraction brief run of the day the
	// pass ran, which the pass updates in place.
	extractionBriefRunSQL = `SELECT r.id FROM unit_briefs b JOIN brief_runs r ON r.id = b.last_run
		WHERE b.provider = ? AND b.unit_key = ? AND r.trigger = 'extraction' AND substr(r.started_at, 1, 10) = substr(?, 1, 10)`
	updateExtractionBriefSQL = `UPDATE brief_runs SET brief = json_object('goal', ?), ended_at = ? WHERE id = ?`
	insertExtractionBriefSQL = `INSERT INTO brief_runs (provider, unit_key, intent, trigger, version, attempt_of, started_at, ended_at, outcome, brief)
		VALUES (?, ?, 'brief', 'extraction', 'extraction', '', ?, ?, 'ok', json_object('goal', ?))`
	useExtractionBriefSQL = `INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES (?, ?, ?, ?)
		ON CONFLICT (provider, unit_key) DO UPDATE SET run = excluded.run, last_run = excluded.last_run`
	setExtractionWatchedSQL = `INSERT INTO extraction_cursors (provider, unit_key, watched, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (provider, unit_key) DO UPDATE SET watched = excluded.watched`
	extractionHintsSQL   = `SELECT item_id, mode, context, why FROM extraction_hints WHERE provider = ? AND unit_key = ?`
	putExtractionHintSQL = `INSERT INTO extraction_hints (provider, unit_key, item_id, mode, context, why) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, unit_key, item_id) DO UPDATE SET mode = excluded.mode, context = excluded.context, why = excluded.why`
	extractionDaySQL   = `SELECT calls, cost_usd FROM extraction_days WHERE day = ?`
	spendExtractionSQL = `INSERT INTO extraction_days (day, calls, cost_usd) VALUES (?, 1, ?)
		ON CONFLICT (day) DO UPDATE SET calls = calls + 1, cost_usd = cost_usd + excluded.cost_usd`
	insertExtractionRunSQL = `INSERT INTO extraction_runs (provider, unit_key, at, model, cost_usd, outcome, error) VALUES (?, ?, ?, ?, ?, ?, ?)`
	openSegmentsOfConvSQL  = `SELECT ` + segmentColumns + ` FROM segment_map WHERE provider = ? AND conversation = ? AND closed = 0
		ORDER BY from_ts, segment_id`
)

func scanExtractionStates(rows *sql.Rows, err error) (map[unit.Unit]ExtractionState, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[unit.Unit]ExtractionState{}
	for rows.Next() {
		var provider, key, at string
		var s ExtractionState
		if err := rows.Scan(&provider, &key, &s.LastTS, &s.LastSeq, &s.Brief, &s.Verdict, &s.VerdictWhy, &s.ViewerRole, &s.Topic,
			&s.StartedByViewer, &s.ClosurePosted, &s.Watched, &s.ClosureMarked, &at); err != nil {
			return nil, err
		}
		s.At = parseTime(at)
		out[unit.Unit{Provider: unit.ProviderID(provider), Key: key}] = s
	}
	return out, rows.Err()
}

// States are the rows of units; a unit without one is missing from the map.
func (s *ExtractionStore) States(ctx context.Context, units []unit.Unit) (map[unit.Unit]ExtractionState, error) {
	if len(units) == 0 {
		return map[unit.Unit]ExtractionState{}, nil
	}
	pairs := make([][2]string, len(units))
	for i, u := range units {
		pairs[i] = [2]string{string(u.Provider), u.Key}
	}
	list, err := json.Marshal(pairs)
	if err != nil {
		return nil, err
	}
	return scanExtractionStates(s.db.R().QueryContext(ctx, extractionStatesSQL, string(list)))
}

// Watched are the rows of provider that SetWatched marked.
func (s *ExtractionStore) Watched(ctx context.Context, provider unit.ProviderID) (map[unit.Unit]ExtractionState, error) {
	return scanExtractionStates(s.db.R().QueryContext(ctx, extractionWatchedSQL, string(provider)))
}

func (s *ExtractionStore) Save(ctx context.Context, sv ExtractionSave) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		for u, st := range sv.States {
			tx.Touch(port.TopicSourceEvents, u)
			if _, err := tx.ExecContext(ctx, putExtractionStateSQL, string(u.Provider), u.Key, st.LastTS, st.LastSeq,
				st.Verdict, st.VerdictWhy, st.ViewerRole, st.Topic, st.StartedByViewer, st.ClosurePosted, formatTime(st.At)); err != nil {
				return err
			}
			if err := putExtractionBrief(ctx, tx, u, st); err != nil {
				return err
			}
		}
		for u, hints := range sv.Hints {
			tx.Touch(port.TopicSourceEvents, u)
			for id, h := range hints {
				if _, err := tx.ExecContext(ctx, putExtractionHintSQL, string(u.Provider), u.Key, id, h.Mode, h.Context, h.Why); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return err
}

// putExtractionBrief keeps st.Brief as u's extraction brief run of the day
// and the brief u uses; an empty Brief keeps the brief u has.
func putExtractionBrief(ctx context.Context, tx *Tx, u unit.Unit, st ExtractionState) error {
	if st.Brief == "" {
		return nil
	}
	at := formatTime(st.At)
	tx.Touch(port.TopicBriefs, u)
	var id int64
	err := tx.QueryRowContext(ctx, extractionBriefRunSQL, string(u.Provider), u.Key, at).Scan(&id)
	switch {
	case err == nil:
		_, err = tx.ExecContext(ctx, updateExtractionBriefSQL, st.Brief, at, id)
		return err
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	res, err := tx.ExecContext(ctx, insertExtractionBriefSQL, string(u.Provider), u.Key, at, at, st.Brief)
	if err != nil {
		return err
	}
	if id, err = res.LastInsertId(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, useExtractionBriefSQL, string(u.Provider), u.Key, id, id)
	return err
}

func (s *ExtractionStore) SetWatched(ctx context.Context, u unit.Unit, watched bool, at time.Time) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, setExtractionWatchedSQL, string(u.Provider), u.Key, watched, formatTime(at))
		return err
	})
	return err
}

// Hints are u's item hints by item id.
func (s *ExtractionStore) Hints(ctx context.Context, u unit.Unit) (map[string]ExtractionHint, error) {
	rows, err := s.db.R().QueryContext(ctx, extractionHintsSQL, string(u.Provider), u.Key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ExtractionHint{}
	for rows.Next() {
		var id string
		var h ExtractionHint
		if err := rows.Scan(&id, &h.Mode, &h.Context, &h.Why); err != nil {
			return nil, err
		}
		out[id] = h
	}
	return out, rows.Err()
}

// Day is the spend of day ("2006-01-02"): calls made and their summed cost.
func (s *ExtractionStore) Day(ctx context.Context, day string) (calls int, costUSD float64, err error) {
	err = s.db.R().QueryRowContext(ctx, extractionDaySQL, day).Scan(&calls, &costUSD)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return calls, costUSD, err
}

// Spend counts one call of costUSD toward day.
func (s *ExtractionStore) Spend(ctx context.Context, day string, costUSD float64) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, spendExtractionSQL, day, costUSD)
		return err
	})
	return err
}

func (s *ExtractionStore) Record(ctx context.Context, r ExtractionRun) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, insertExtractionRunSQL, string(r.Unit.Provider), r.Unit.Key, formatTime(r.At), r.Model, r.CostUSD,
			r.Outcome, r.Error)
		return err
	})
	return err
}

// Of are conv's open segments, ascending.
func (s *SegmentStore) Of(ctx context.Context, provider unit.ProviderID, conv string) ([]SegmentRow, error) {
	return scanSegments(s.db.R().QueryContext(ctx, openSegmentsOfConvSQL, string(provider), conv))
}
