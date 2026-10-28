package store

import "context"

// ViewerKey is the meta key of the viewer's login, as the host's store kept
// it before the plugin had a store.
const ViewerKey = "viewer:" + "github"

// Meta is key's value, "" when unset. Meta values change no reported fact.
func (db *DB) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := db.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if noRows(err) {
		return "", nil
	}
	return v, err
}

// MetaPrefixed is every meta value whose key starts with prefix, by key.
func (db *DB) MetaPrefixed(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := db.db.QueryContext(ctx, `SELECT key, value FROM meta WHERE substr(key, 1, length(?)) = ?`, prefix, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (db *DB) SetMeta(ctx context.Context, key, value string) error {
	return db.SetMetas(ctx, map[string]string{key: value})
}

// SetMetas writes every key of kv in one transaction.
func (db *DB) SetMetas(ctx context.Context, kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	_, err := db.Write(ctx, func(tx *Tx) error {
		for k, v := range kv {
			if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
				ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE value IS NOT excluded.value`, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// Viewer is the stored viewer login, "" before it is known.
func (db *DB) Viewer(ctx context.Context) (string, error) { return db.Meta(ctx, ViewerKey) }

func (db *DB) SetViewer(ctx context.Context, login string) error {
	return db.SetMeta(ctx, ViewerKey, login)
}
