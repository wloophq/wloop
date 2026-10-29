package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type itemStore struct{ db *DB }

var _ coreitems.Store = itemStore{}

// Items is the items ledger's store: items, their op log and proposals.
func (db *DB) Items() coreitems.Store { return itemStore{db} }

const (
	itemsOfUnitSQL = `SELECT body FROM items WHERE provider = ? AND unit_key = ? ORDER BY created_at, id`
	itemsQuerySQL  = `SELECT body FROM items`
	itemsOrder     = ` ORDER BY provider, unit_key, created_at, id`
	upsertItemSQL  = `INSERT INTO items (provider, unit_key, id, body, state, confirmed, provenance, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, unit_key, id) DO UPDATE SET body = excluded.body, state = excluded.state,
		confirmed = excluded.confirmed, provenance = excluded.provenance,
		updated_at = excluded.updated_at`
	insertItemOpSQL = `INSERT INTO item_ops (provider, unit_key, item, op, provenance, outcome, reason, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	itemOpsSQL      = `SELECT item, op, provenance, outcome, reason, at FROM item_ops WHERE provider = ? AND unit_key = ? AND at >= ? ORDER BY seq`
	insertProposal  = `INSERT INTO item_proposals (provider, unit_key, item, op, provenance, reason, dedupe, at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
)

// proposalsSQL are the item proposals, each in the state of its newest gate not superseded: the gate of its ledger
// message, or the one it has before its message exists. A superseded gate decides nothing, such as one a reconcile
// opened while the proposal's decision was being applied. A proposal with no gate open or decided is open.
var proposalsSQL = fmt.Sprintf(`SELECT id, provider, unit_key, item, op, provenance, reason, state, at, decided_at != '' FROM (
	SELECT p.*, CASE coalesce((SELECT g.status FROM gates g WHERE g.provider = p.provider AND g.unit_key = p.unit_key AND g.kind = '%s'
		AND g.status != '%s'
		AND g.subject IN ('%s' || p.id, (SELECT CAST(m.id AS TEXT) FROM messages m WHERE m.origin_kind = '%s' AND m.proposal = p.id))
		ORDER BY g.id DESC LIMIT 1), '')
	WHEN '%s' THEN '%s' WHEN '%s' THEN '%s' WHEN '%s' THEN '%s' ELSE '%s' END AS state
	FROM item_proposals p)`,
	gate.KindProposal, gate.Superseded, strings.TrimSuffix(gate.ProposalSubject(0), "0"), message.OriginLedger,
	gate.Approved, coreitems.ProposalAccepted, gate.Rejected, coreitems.ProposalRejected, gate.Dismissed, coreitems.ProposalRejected,
	coreitems.ProposalOpen)

func (s itemStore) Load(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error) {
	out := make(map[unit.Unit][]items.Item, len(units))
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		for _, u := range units {
			if _, done := out[u]; done {
				continue
			}
			its, err := scanItems(tx.QueryContext(ctx, itemsOfUnitSQL, string(u.Provider), u.Key))
			if err != nil {
				return fmt.Errorf("items of %s: %w", u, err)
			}
			if len(its) > 0 {
				out[u] = its
			}
		}
		return nil
	})
	return out, err
}

func scanItems(rows *sql.Rows, err error) ([]items.Item, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []items.Item
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var it items.Item
		if err := json.Unmarshal([]byte(body), &it); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s itemStore) Query(ctx context.Context, q coreitems.Query) ([]items.Item, error) {
	var where []string
	var args []any
	if q.State != "" {
		where, args = append(where, "state = ?"), append(args, string(q.State))
	}
	if len(q.Providers) > 0 {
		list, _ := json.Marshal(q.Providers)
		where, args = append(where, "provider IN (SELECT value FROM json_each(?))"), append(args, string(list))
	}
	if len(q.Units) > 0 {
		var ors []string
		for _, u := range q.Units {
			ors, args = append(ors, "(provider = ? AND unit_key = ?)"), append(args, string(u.Provider), u.Key)
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	query := itemsQuerySQL
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	return scanItems(s.db.R().QueryContext(ctx, query+itemsOrder, args...))
}

func (s itemStore) Proposals(ctx context.Context, state coreitems.ProposalState) ([]coreitems.Proposal, error) {
	query, args := proposalsSQL+` ORDER BY id`, []any(nil)
	if state != "" {
		query, args = proposalsSQL+` WHERE state = ? ORDER BY id`, []any{string(state)}
	}
	rows, err := s.db.R().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []coreitems.Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanProposal(r scanner) (coreitems.Proposal, error) {
	var p coreitems.Proposal
	var provider, op, prov, state, at string
	if err := r.Scan(&p.ID, &provider, &p.Unit.Key, &p.Item, &op, &prov, &p.Reason, &state, &at, &p.Decided); err != nil {
		return p, err
	}
	p.Unit.Provider, p.Provenance, p.State = unit.ProviderID(provider), items.Provenance(prov), coreitems.ProposalState(state)
	if err := json.Unmarshal([]byte(op), &p.Op); err != nil {
		return p, fmt.Errorf("proposal %d: %w", p.ID, err)
	}
	var err error
	if p.At, err = time.Parse(TimeLayout, at); err != nil {
		return p, fmt.Errorf("proposal %d: %w", p.ID, err)
	}
	return p, nil
}

func (s itemStore) Log(ctx context.Context, u unit.Unit, since time.Time) ([]coreitems.LogEntry, error) {
	floor := ""
	if !since.IsZero() {
		floor = since.UTC().Format("2006-01-02T15:04:05")
	}
	rows, err := s.db.R().QueryContext(ctx, itemOpsSQL, string(u.Provider), u.Key, floor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []coreitems.LogEntry
	for rows.Next() {
		e := coreitems.LogEntry{Unit: u}
		var op, prov, at string
		if err := rows.Scan(&e.Item, &op, &prov, &e.Outcome, &e.Reason, &at); err != nil {
			return nil, err
		}
		e.Provenance = items.Provenance(prov)
		if err := json.Unmarshal([]byte(op), &e.Op); err != nil {
			return nil, err
		}
		if e.At, err = time.Parse(TimeLayout, at); err != nil {
			return nil, err
		}
		if e.At.Before(since) {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s itemStore) Commit(ctx context.Context, c coreitems.Commit) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		for _, it := range c.Items {
			body, err := json.Marshal(it)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, upsertItemSQL, string(it.Unit.Provider), it.Unit.Key, it.ID, string(body),
				string(it.State), it.Confirmed, string(it.Provenance),
				formatTime(it.CreatedAt), formatTime(it.UpdatedAt)); err != nil {
				return fmt.Errorf("item %s %s: %w", it.Unit, it.ID, err)
			}
			tx.Touch(port.TopicItems, it.Unit)
		}
		for _, e := range c.Log {
			op, err := json.Marshal(e.Op)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, insertItemOpSQL, string(e.Unit.Provider), e.Unit.Key, e.Item, string(op),
				string(e.Provenance), e.Outcome, e.Reason, formatTime(e.At)); err != nil {
				return err
			}
		}
		for _, p := range c.Proposals {
			if err := saveProposal(ctx, tx, p); err != nil {
				return err
			}
		}
		for _, id := range c.Decided {
			if _, err := tx.ExecContext(ctx, `UPDATE item_proposals SET decided_at = ? WHERE id = ?`, formatTime(time.Now()), id); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// saveProposal stores a new proposal; a proposal already stored is decided by its gate, which the store keeps.
func saveProposal(ctx context.Context, tx *Tx, p coreitems.Proposal) error {
	if p.ID != 0 {
		return nil
	}
	op, err := json.Marshal(p.Op)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, insertProposal, string(p.Unit.Provider), p.Unit.Key, p.Item, string(op),
		string(p.Provenance), p.Reason, coreitems.DedupeKey(p.Op), formatTime(p.At)); err != nil {
		return fmt.Errorf("proposal on %s %s: %w", p.Unit, p.Item, err)
	}
	tx.Touch(port.TopicItems, p.Unit)
	return nil
}
