package sqlite

import (
	"context"
	"database/sql"
)

// v7Briefs makes every brief in use a record of kind brief on its unit, an event the timeline reads, its
// tags those unit_tags kept; unit_tags goes. A brief the extraction or a plugin wrote is the plugin's, every
// other the agent's. brief_runs and unit_briefs stay, as the run log.
func v7Briefs(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data)
		SELECT b.provider, b.unit_key, 'record', 'brief', iif(r.trigger IN ('extraction', 'plugin'), 'plugin', 'agent'),
			iif(r.ended_at != '', r.ended_at, r.started_at),
			json_set(r.brief, '$.tags', json((SELECT json_group_array(tag) FROM (SELECT t.key || ':' || t.value AS tag FROM unit_tags t
				WHERE t.provider = b.provider AND t.unit_key = b.unit_key ORDER BY t.key, t.value))))
		FROM unit_briefs b JOIN brief_runs r ON r.id = b.run
		WHERE b.run != 0 AND json_valid(r.brief) AND json_type(r.brief) = 'object'
		ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DROP TABLE unit_tags`)
	return err
}
