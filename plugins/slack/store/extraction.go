package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

// ExtractionStore holds what the Slack extraction job keeps per unit: its cursor and last
// answer, the hints of each item, the day's spend and a log of its calls.
type ExtractionStore struct{ db *DB }

func (db *DB) Extraction() *ExtractionStore { return &ExtractionStore{db} }

// ExtractionState is one unit's row. Watched is written only by SetWatched;
// ClosureMarked, a sent draft's unit.closed, never by Save.
//
// Brief is the JSON of the brief the extraction last wrote of the unit; an
// empty Brief in a Save keeps the one stored.
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

// ExtractionSave is one run's result: the cursors, briefs and hints of its
// units, in one transaction.
type ExtractionSave struct {
	States map[unit.Unit]ExtractionState
	// Hints replace the hints of the items they name.
	Hints map[unit.Unit]map[string]ExtractionHint
}

const (
	extractionStateColumns = `provider, unit_key, last_ts, last_seq,
		verdict, verdict_why, viewer_role, topic, started_by_viewer, closure_posted, watched, closure_marked, at, brief`
	extractionStatesSQL = `SELECT ` + extractionStateColumns + ` FROM extraction_cursors
		WHERE (provider, unit_key) IN (SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]') FROM json_each(?))`
	extractionWatchedSQL  = `SELECT ` + extractionStateColumns + ` FROM extraction_cursors WHERE provider = ? AND watched = 1`
	putExtractionStateSQL = `INSERT INTO extraction_cursors (provider, unit_key, last_ts, last_seq, verdict, verdict_why,
		viewer_role, topic, started_by_viewer, closure_posted, at, brief) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, unit_key) DO UPDATE SET last_ts = excluded.last_ts, last_seq = excluded.last_seq,
		verdict = excluded.verdict, verdict_why = excluded.verdict_why,
		viewer_role = excluded.viewer_role, topic = excluded.topic, started_by_viewer = excluded.started_by_viewer,
		closure_posted = excluded.closure_posted, at = excluded.at,
		brief = CASE WHEN excluded.brief = '' THEN extraction_cursors.brief ELSE excluded.brief END`
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
		if err := rows.Scan(&provider, &key, &s.LastTS, &s.LastSeq, &s.Verdict, &s.VerdictWhy, &s.ViewerRole, &s.Topic,
			&s.StartedByViewer, &s.ClosurePosted, &s.Watched, &s.ClosureMarked, &at, &s.Brief); err != nil {
			return nil, err
		}
		s.At = parseTime(at)
		out[unit.Unit{Provider: unit.ProviderID(provider), Key: key}] = s
	}
	return out, rows.Err()
}

// unitList is the bound JSON list of units.
func unitList(units []unit.Unit) (string, error) {
	pairs := make([][2]string, len(units))
	for i, u := range units {
		pairs[i] = [2]string{string(u.Provider), u.Key}
	}
	list, err := json.Marshal(pairs)
	return string(list), err
}

// States are the rows of units; a unit without one is missing from the map.
func (s *ExtractionStore) States(ctx context.Context, units []unit.Unit) (map[unit.Unit]ExtractionState, error) {
	if len(units) == 0 {
		return map[unit.Unit]ExtractionState{}, nil
	}
	list, err := unitList(units)
	if err != nil {
		return nil, err
	}
	return scanExtractionStates(s.db.R().QueryContext(ctx, extractionStatesSQL, list))
}

// Watched are the rows of provider that SetWatched marked.
func (s *ExtractionStore) Watched(ctx context.Context, provider unit.ProviderID) (map[unit.Unit]ExtractionState, error) {
	return scanExtractionStates(s.db.R().QueryContext(ctx, extractionWatchedSQL, string(provider)))
}

func (s *ExtractionStore) Save(ctx context.Context, sv ExtractionSave) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		for u, st := range sv.States {
			tx.Touch(u)
			if _, err := tx.ExecContext(ctx, putExtractionStateSQL, string(u.Provider), u.Key, st.LastTS, st.LastSeq,
				st.Verdict, st.VerdictWhy, st.ViewerRole, st.Topic, st.StartedByViewer, st.ClosurePosted, formatTime(st.At), st.Brief); err != nil {
				return err
			}
		}
		for u, hints := range sv.Hints {
			tx.Touch(u)
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
