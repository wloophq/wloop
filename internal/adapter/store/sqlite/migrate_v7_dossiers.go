package sqlite

import (
	"context"
	"database/sql"
)

// v7Dossiers drops projects, which kept only the slugs of the projects' dossiers. The dossier rows stay as
// version 6 left them: version 15 drops their tables.
func v7Dossiers(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE projects`)
	return err
}
