package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type linkStore struct{ db *DB }

var _ port.LinkStore = linkStore{}

// Links is the store of cross-source links.
func (db *DB) Links() port.LinkStore { return linkStore{db} }

const (
	linkColumns  = `provider, unit_key, to_provider, to_key, kind, item, evidence, origin, at`
	linksFromSQL = `SELECT ` + linkColumns + ` FROM links WHERE provider = ? AND unit_key = ? ORDER BY at, to_provider, to_key, kind, item`
	linksToSQL   = `SELECT ` + linkColumns + ` FROM links
		WHERE (to_provider, to_key) IN (SELECT value->>0, value->>1 FROM json_each(?))
		ORDER BY provider, unit_key, at, to_provider, to_key, kind, item`
	linkTargetsSQL = `SELECT DISTINCT to_provider, to_key FROM links WHERE item != '' AND kind IN ('review', 'merge')
		ORDER BY to_provider, to_key`
	insertFactsLinkSQL = `INSERT INTO links (` + linkColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`
	updateLinkEvidence = `UPDATE links SET evidence = ? WHERE provider = ? AND unit_key = ? AND to_provider = ? AND to_key = ?
		AND kind = ? AND item = ? AND origin = 'facts' AND evidence != ?`
	deleteLinkSQL = `DELETE FROM links WHERE provider = ? AND unit_key = ? AND to_provider = ? AND to_key = ? AND kind = ? AND item = ?`
	putLinkSQL    = `INSERT INTO links (` + linkColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, unit_key, to_provider, to_key, kind, item) DO UPDATE SET evidence = excluded.evidence,
		origin = CASE WHEN links.origin = 'user' THEN 'user' ELSE excluded.origin END
		WHERE links.evidence != excluded.evidence OR (links.origin != excluded.origin AND links.origin != 'user')`
)

type linkKey struct {
	from, to unit.Unit
	kind     link.Kind
	item     string
}

func keyOf(l link.Link) linkKey { return linkKey{l.From, l.To, l.Kind, l.Item} }

func (s linkStore) ReplaceFacts(ctx context.Context, from []unit.Unit, links []link.Link, at time.Time) error {
	in := make(map[unit.Unit]bool, len(from))
	for _, u := range from {
		in[u] = true
	}
	want := map[linkKey]link.Link{}
	var order []linkKey
	for _, l := range links {
		if !in[l.From] {
			return fmt.Errorf("links: %s links from %s, outside the replaced units", l.To, l.From)
		}
		if !l.Kind.Valid() {
			return fmt.Errorf("links: %s -> %s: unknown kind %q", l.From, l.To, l.Kind)
		}
		k := keyOf(l)
		if _, seen := want[k]; !seen {
			order = append(order, k)
		}
		want[k] = l
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		for u := range in {
			have, err := scanLinks(tx.QueryContext(ctx, linksFromSQL, string(u.Provider), u.Key))
			if err != nil {
				return fmt.Errorf("links from %s: %w", u, err)
			}
			for _, h := range have {
				if _, kept := want[keyOf(h)]; kept || h.Origin != link.Facts {
					continue
				}
				if _, err := tx.ExecContext(ctx, deleteLinkSQL, linkKeyArgs(h)...); err != nil {
					return err
				}
				tx.Touch(port.TopicLinks, h.From, h.To)
			}
		}
		for _, k := range order {
			l := want[k]
			l.Origin = link.Facts
			if l.At.IsZero() {
				l.At = at
			}
			ev, err := json.Marshal(l.Evidence)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, insertFactsLinkSQL, linkArgs(l, string(ev))...)
			if err != nil {
				return fmt.Errorf("link %s -> %s: %w", l.From, l.To, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				tx.Touch(port.TopicLinks, l.From, l.To)
				continue
			}
			args := append([]any{string(ev)}, linkKeyArgs(l)...)
			res, err = tx.ExecContext(ctx, updateLinkEvidence, append(args, string(ev))...)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				tx.Touch(port.TopicLinks, l.From, l.To)
			}
		}
		return nil
	})
	return err
}

func (s linkStore) Put(ctx context.Context, l link.Link) error {
	switch {
	case l.Origin != link.Extraction && l.Origin != link.User:
		return fmt.Errorf("links: put of origin %q; only extraction and user", l.Origin)
	case !l.Kind.Valid():
		return fmt.Errorf("links: unknown kind %q", l.Kind)
	case l.From.IsZero() || l.To.IsZero():
		return fmt.Errorf("links: a link needs a from and a to unit")
	}
	ev, err := json.Marshal(l.Evidence)
	if err != nil {
		return err
	}
	_, err = s.db.Write(ctx, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, putLinkSQL, linkArgs(l, string(ev))...)
		if err != nil {
			return fmt.Errorf("link %s -> %s: %w", l.From, l.To, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			tx.Touch(port.TopicLinks, l.From, l.To)
		}
		return nil
	})
	return err
}

// LinkTargets are the units some link that can resolve an item points to,
// sorted by provider and key.
func (db *DB) LinkTargets(ctx context.Context) ([]unit.Unit, error) {
	rows, err := db.R().QueryContext(ctx, linkTargetsSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []unit.Unit
	for rows.Next() {
		var u unit.Unit
		var p string
		if err := rows.Scan(&p, &u.Key); err != nil {
			return nil, err
		}
		u.Provider = unit.ProviderID(p)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s linkStore) From(ctx context.Context, u unit.Unit) ([]link.Link, error) {
	return scanLinks(s.db.R().QueryContext(ctx, linksFromSQL, string(u.Provider), u.Key))
}

func (s linkStore) To(ctx context.Context, units []unit.Unit) ([]link.Link, error) {
	if len(units) == 0 {
		return nil, nil
	}
	keys := make([][2]string, len(units))
	for i, u := range units {
		keys[i] = [2]string{string(u.Provider), u.Key}
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	return scanLinks(s.db.R().QueryContext(ctx, linksToSQL, string(raw)))
}

func linkArgs(l link.Link, evidence string) []any {
	return []any{string(l.From.Provider), l.From.Key, string(l.To.Provider), l.To.Key, string(l.Kind), l.Item,
		evidence, string(l.Origin), formatTime(l.At)}
}

func linkKeyArgs(l link.Link) []any {
	return []any{string(l.From.Provider), l.From.Key, string(l.To.Provider), l.To.Key, string(l.Kind), l.Item}
}

func scanLinks(rows *sql.Rows, err error) ([]link.Link, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []link.Link
	for rows.Next() {
		var l link.Link
		var fromP, toP, kind, ev, origin, at string
		if err := rows.Scan(&fromP, &l.From.Key, &toP, &l.To.Key, &kind, &l.Item, &ev, &origin, &at); err != nil {
			return nil, err
		}
		l.From.Provider, l.To.Provider, l.Kind, l.Origin = unit.ProviderID(fromP), unit.ProviderID(toP), link.Kind(kind), link.Origin(origin)
		if err := json.Unmarshal([]byte(ev), &l.Evidence); err != nil {
			return nil, fmt.Errorf("link %s -> %s: %w", l.From, l.To, err)
		}
		l.At = parseTime(at)
		out = append(out, l)
	}
	return out, rows.Err()
}
