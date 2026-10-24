package sqlite

import (
	"context"
	"database/sql"
)

func execW(db *DB, query string, args ...any) (sql.Result, error) {
	var res sql.Result
	_, err := db.Write(context.Background(), func(tx *Tx) error {
		var err error
		res, err = tx.ExecContext(context.Background(), query, args...)
		return err
	})
	return res, err
}
