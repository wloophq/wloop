package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// SegmentStore is the DM segment map: a conversation's ts ranges, each under an id that
// is never renamed or reused. Ranges are inclusive at both ends; ToTS "" is open.
type SegmentStore struct{ db *DB }

func (db *DB) Segments() *SegmentStore { return &SegmentStore{db} }

// SegmentSpan is a range Apply writes; ID "" asks for a new segment.
type SegmentSpan struct{ ID, FromTS, ToTS string }

type SegmentRow struct {
	Team, Conversation, ID, FromTS, ToTS string
	Closed                               bool
}

// SegmentUnit names the unit of a segment, for the notes of Apply.
type SegmentUnit func(team, conv, id string) unit.Unit

var ErrUnknownSegment = errors.New("unknown segment")

const (
	segmentColumns    = `team, conversation, segment_id, from_ts, to_ts, closed`
	segmentsOfConvSQL = `SELECT ` + segmentColumns + ` FROM segment_map WHERE provider = ? AND conversation = ? ORDER BY from_ts, segment_id`
	segmentsCovering  = `SELECT ` + segmentColumns + ` FROM segment_map WHERE provider = ? AND conversation = ? AND closed = 0
		AND from_ts <= ? AND (to_ts = '' OR to_ts >= ?) ORDER BY from_ts, segment_id`
	segmentByIDSQL   = `SELECT ` + segmentColumns + ` FROM segment_map WHERE provider = ? AND conversation = ? AND segment_id = ?`
	insertSegmentSQL = `INSERT INTO segment_map (provider, conversation, segment_id, team, from_ts, to_ts, created_at, updated_at, closed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`
	moveSegmentSQL  = `UPDATE segment_map SET from_ts = ?, to_ts = ?, updated_at = ? WHERE provider = ? AND conversation = ? AND segment_id = ?`
	closeSegmentSQL = `UPDATE segment_map SET closed = 1, updated_at = ? WHERE provider = ? AND conversation = ? AND segment_id = ?`
)

func scanSegment(r scanner) (SegmentRow, error) {
	var s SegmentRow
	err := r.Scan(&s.Team, &s.Conversation, &s.ID, &s.FromTS, &s.ToTS, &s.Closed)
	return s, err
}

func scanSegments(rows *sql.Rows, err error) ([]SegmentRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SegmentRow
	for rows.Next() {
		s, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Covering returns the open segments of conv whose range holds ts.
func (s *SegmentStore) Covering(ctx context.Context, provider unit.ProviderID, conv, ts string) ([]SegmentRow, error) {
	return scanSegments(s.db.R().QueryContext(ctx, segmentsCovering, string(provider), conv, ts, ts))
}

// Segment returns one segment, closed or not; ErrUnknownSegment when conv has no such id.
func (s *SegmentStore) Segment(ctx context.Context, provider unit.ProviderID, conv, id string) (SegmentRow, error) {
	row, err := scanSegment(s.db.R().QueryRowContext(ctx, segmentByIDSQL, string(provider), conv, id))
	if errors.Is(err, sql.ErrNoRows) {
		return row, fmt.Errorf("%w %s in %s", ErrUnknownSegment, id, conv)
	}
	return row, err
}

// Apply replaces conv's segments from spans[0].FromTS on: a named id moves to its span, a
// span without an id adopts the open segment that starts at its FromTS or gets a new id, and
// an open segment of the replaced range that no span names is closed with its last range. Spans are ascending and disjoint; only the last may be
// open. A segment that starts before the replaced range and reaches into it must be named.
// It notes port.TopicSourceEvents on every segment whose range moved, that was minted or
// that was closed, and returns the rows of spans in order.
func (s *SegmentStore) Apply(ctx context.Context, provider unit.ProviderID, team, conv string, spans []SegmentSpan, unitOf SegmentUnit, now time.Time) ([]SegmentRow, error) {
	if err := checkSpans(spans); err != nil {
		return nil, fmt.Errorf("segments of %s: %w", conv, err)
	}
	spans = slices.Clone(spans)
	out := make([]SegmentRow, len(spans))
	at := formatTime(now)
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		have, err := scanSegments(tx.QueryContext(ctx, segmentsOfConvSQL, string(provider), conv))
		if err != nil {
			return err
		}
		byID := make(map[string]SegmentRow, len(have))
		for _, r := range have {
			byID[r.ID] = r
		}
		named := map[string]bool{}
		for _, sp := range spans {
			if sp.ID == "" {
				continue
			}
			r, ok := byID[sp.ID]
			switch {
			case !ok:
				return fmt.Errorf("%w %s in %s", ErrUnknownSegment, sp.ID, conv)
			case r.Closed:
				return fmt.Errorf("segment %s of %s is closed", sp.ID, conv)
			case named[sp.ID]:
				return fmt.Errorf("segment %s of %s is named twice", sp.ID, conv)
			}
			named[sp.ID] = true
		}
		start := spans[0].FromTS
		for i, sp := range spans {
			if sp.ID != "" {
				continue
			}
			for _, r := range have {
				if !r.Closed && !named[r.ID] && r.FromTS == sp.FromTS {
					spans[i].ID, named[r.ID] = r.ID, true
					break
				}
			}
		}
		for _, r := range have {
			if r.Closed || named[r.ID] {
				continue
			}
			if r.FromTS >= start {
				if _, err := tx.ExecContext(ctx, closeSegmentSQL, at, string(provider), conv, r.ID); err != nil {
					return err
				}
				tx.Touch(port.TopicSourceEvents, unitOf(r.Team, conv, r.ID))
				continue
			}
			if r.ToTS == "" || r.ToTS >= start {
				return fmt.Errorf("segment %s of %s reaches into the range from %s and is not named", r.ID, conv, start)
			}
		}
		for i, sp := range spans {
			if sp.ID == "" {
				sp.ID = mintSegmentID(sp.FromTS, byID)
				if _, err := tx.ExecContext(ctx, insertSegmentSQL, string(provider), conv, sp.ID, team, sp.FromTS, sp.ToTS, at, at); err != nil {
					return err
				}
				byID[sp.ID] = SegmentRow{ID: sp.ID}
				tx.Touch(port.TopicSourceEvents, unitOf(team, conv, sp.ID))
				out[i] = SegmentRow{Team: team, Conversation: conv, ID: sp.ID, FromTS: sp.FromTS, ToTS: sp.ToTS}
				continue
			}
			r := byID[sp.ID]
			if r.FromTS != sp.FromTS || r.ToTS != sp.ToTS {
				if _, err := tx.ExecContext(ctx, moveSegmentSQL, sp.FromTS, sp.ToTS, at, string(provider), conv, sp.ID); err != nil {
					return err
				}
				tx.Touch(port.TopicSourceEvents, unitOf(r.Team, conv, sp.ID))
			}
			out[i] = SegmentRow{Team: r.Team, Conversation: conv, ID: sp.ID, FromTS: sp.FromTS, ToTS: sp.ToTS}
		}
		return rehomeDrafts(ctx, tx, provider, team, conv, out, unitOf)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func checkSpans(spans []SegmentSpan) error {
	if len(spans) == 0 {
		return errors.New("no spans")
	}
	for i, sp := range spans {
		switch {
		case sp.FromTS == "":
			return fmt.Errorf("span %d has no from_ts", i)
		case sp.ToTS != "" && sp.ToTS < sp.FromTS:
			return fmt.Errorf("span %d ends at %s before it starts at %s", i, sp.ToTS, sp.FromTS)
		case i == len(spans)-1:
		case sp.ToTS == "":
			return fmt.Errorf("span %d is open but not the last", i)
		case sp.ToTS >= spans[i+1].FromTS:
			return fmt.Errorf("span %d ends at %s, not before span %d starts at %s", i, sp.ToTS, i+1, spans[i+1].FromTS)
		}
	}
	return nil
}

// mintSegmentID is fromTS without its dot, suffixed "-<n>" when conv held that id before.
func mintSegmentID(fromTS string, taken map[string]SegmentRow) string {
	base := strings.Replace(fromTS, ".", "", 1)
	id := base
	for n := 2; ; n++ {
		if _, ok := taken[id]; !ok {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// draftsOfConvSQL reads the drafts on the units of a conversation: the
// unit keys "<team>/<conv>/..." from ?2 up to ?3.
const draftsOfConvSQL = `SELECT id, unit_key FROM drafts WHERE provider = ?1 AND unit_key >= ?2 AND unit_key < ?3`

// rehomeDrafts moves the drafts a session recorded on a direct message
// before it was segmented, each on the thread-shaped unit of its message,
// onto the segment of rows whose range holds that message.
func rehomeDrafts(ctx context.Context, tx *Tx, provider unit.ProviderID, team, conv string, rows []SegmentRow, unitOf SegmentUnit) error {
	prefix := team + "/" + conv + "/"
	list, err := tx.QueryContext(ctx, draftsOfConvSQL, string(provider), prefix, team+"/"+conv+"0")
	if err != nil {
		return err
	}
	moves := map[int64]unit.Unit{}
	for list.Next() {
		var id int64
		var key string
		if err := list.Scan(&id, &key); err != nil {
			list.Close()
			return err
		}
		ts := strings.TrimPrefix(key, prefix)
		if strings.Contains(ts, "/") {
			continue
		}
		for _, r := range rows {
			if ts >= r.FromTS && (r.ToTS == "" || ts <= r.ToTS) {
				moves[id] = unitOf(team, conv, r.ID)
				break
			}
		}
	}
	list.Close()
	if err := list.Err(); err != nil {
		return err
	}
	for id, u := range moves {
		if _, err := tx.ExecContext(ctx, `UPDATE drafts SET unit_key = ?, rev = rev + 1 WHERE id = ?`, u.Key, id); err != nil {
			return err
		}
		tx.Touch(port.TopicDrafts, u)
	}
	return nil
}
