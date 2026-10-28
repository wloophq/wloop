package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// migration takes a store from version to-1 to to, in one transaction that
// also sets the version.
type migration struct {
	to   uint32
	name string
	// foreignKeysOff runs the migration with foreign key enforcement off, for
	// a table rebuild; it fails when it leaves more violations than it found.
	foreignKeysOff bool
	// compatible is the lowest version a release must read to keep using the
	// store after this migration; 0 is to itself. A migration that only adds
	// what an older release ignores declares that release's version.
	compatible uint32
	up         func(ctx context.Context, tx *sql.Tx) error
}

func (m migration) label() string { return fmt.Sprintf("%04d_%s", m.to, m.name) }

// migrations take a store from version 1 to SchemaVersion, one version each,
// in order. A migration in a release is never edited: a fix is a new one.
var migrations = []migration{
	{to: 2, name: "draft_shape", compatible: 1, up: execAll(
		`ALTER TABLE drafts ADD COLUMN purpose TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE drafts ADD COLUMN delivery TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE drafts ADD COLUMN effects TEXT NOT NULL DEFAULT '[]'`,
		`UPDATE drafts SET purpose = kind, delivery = CASE kind WHEN 'react' THEN 'reaction' WHEN 'comment' THEN 'comment' ELSE 'message' END`,
		`UPDATE drafts SET effects = '["entry.nudged"]' WHERE kind = 'nudge' AND state IN ('proposed', 'approved', 'sending', 'failed')`,
		`UPDATE drafts SET effects = '["unit.closed"]' WHERE kind = 'close' AND state IN ('proposed', 'approved', 'sending', 'failed')`,
		`UPDATE extraction_cursors SET closure_posted = 1 WHERE closure_posted = 0 AND EXISTS (
			SELECT 1 FROM drafts d WHERE d.provider = extraction_cursors.provider AND d.unit_key = extraction_cursors.unit_key
			AND d.kind = 'close' AND d.state = 'sent')`,
	)},
	{to: 3, name: "unit_marks", up: execAll(
		`CREATE TABLE unit_marks (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  mark TEXT NOT NULL CHECK (mark != ''),
  value TEXT NOT NULL CHECK (value != ''),
  PRIMARY KEY (provider, unit_key, mark)
) WITHOUT ROWID`,
		`INSERT INTO unit_marks (provider, unit_key, mark, value)
		WITH m(mark) AS (VALUES ('reviewed'), ('addressed.reviews'), ('addressed.checks'))
		SELECT r.provider, r.unit_key, m.mark, CASE m.mark WHEN 'addressed.checks' THEN r.checks_head ELSE r.fingerprint END AS value
		FROM round_state r CROSS JOIN m WHERE value != ''
		AND NOT (m.mark = 'addressed.reviews' AND instr(r.fingerprint, '@') > 0)`,
		`ALTER TABLE round_state DROP COLUMN fingerprint`,
		`ALTER TABLE round_state DROP COLUMN checks_head`,
	)},
	{to: 4, name: "units_everywhere", up: v4},
	{to: 5, name: "draft_origin_and_replaced", compatible: 4, up: execAll(
		`ALTER TABLE drafts ADD COLUMN origin_provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE drafts ADD COLUMN origin_unit_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE drafts ADD COLUMN replaced TEXT NOT NULL DEFAULT ''`,
	)},
	{to: 6, name: "answer_message", compatible: 4, up: v6Answers},
	{to: 7, name: "tasks", foreignKeysOff: true, compatible: 7, up: v7},
}

// schemaSet is a schema and the migrations that reach it, so tests can run
// the procedure on versions this release does not have.
type schemaSet struct {
	version    uint32
	schema     string
	migrations []migration
}

var current = schemaSet{SchemaVersion, schema, migrations}

// compatibleAt is the compatible version of a store at version v.
func (set schemaSet) compatibleAt(v uint32) uint32 {
	for _, m := range set.migrations {
		if m.to == v && m.compatible != 0 {
			return m.compatible
		}
	}
	return v
}

func (set schemaSet) pending(from uint32) []migration {
	var out []migration
	for _, m := range set.migrations {
		if m.to > from {
			out = append(out, m)
		}
	}
	return out
}

// execAll is a migration's up that runs each statement in turn.
func execAll(stmts ...string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range stmts {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("%s: %w", strings.Join(strings.Fields(q), " "), err)
			}
		}
		return nil
	}
}

// Plan is what Migrate does to a store.
type Plan struct {
	From, To uint32
	// Steps are the migrations it runs, as 0002_name.
	Steps []string
	// Backup is the file it copies the store to first.
	Backup string
	// Size is the store's size with its WAL, and Free the bytes free where
	// the backup goes, -1 when unknown.
	Size, Free int64
}

// Pending says the plan migrates anything.
func (p Plan) Pending() bool { return len(p.Steps) > 0 }

// Short says the free space cannot hold the backup with room to spare.
func (p Plan) Short() bool { return p.Free >= 0 && p.Free < 2*p.Size }

// PlanMigration is what Migrate would do to the store at path, with its
// backup in the directory backups; it changes nothing. A store this release
// cannot migrate is a SchemaError.
func PlanMigration(path, backups string, now time.Time) (Plan, error) {
	return current.plan(path, backups, now)
}

func (set schemaSet) plan(path, backups string, now time.Time) (Plan, error) {
	s, err := set.inspect(path)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{From: s.Version, To: s.Version, Free: -1}
	switch s.stateFor(set.version) {
	case StateCurrent, StateReadable:
		return p, nil
	case StateOlder:
	default:
		return p, SchemaError{path, s, set.version}
	}
	p.To = set.version
	for _, m := range set.pending(s.Version) {
		p.Steps = append(p.Steps, m.label())
	}
	for _, f := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(f); err == nil {
			p.Size += fi.Size()
		}
	}
	if p.Backup, err = backupName(backups, s.Version, now, false); err != nil {
		return p, err
	}
	p.Free = freeSpace(backups)
	return p, nil
}

// MigrateOptions are where Migrate keeps backups and what it reports.
type MigrateOptions struct {
	Backups string
	Now     func() time.Time
	// Keep is how many backups taken before a migration stay; 0 keeps
	// DefaultKeep.
	Keep int
	// Backup, when set, is the backup's path, as a Plan named it.
	Backup string
	// Step, when set, hears of each migration as it commits.
	Step func(label string, took time.Duration)
	// Warn, when set, hears of what failed after the migrations committed
	// without undoing them, such as removing an old backup.
	Warn func(error)
}

// DefaultKeep is how many pre-migration backups stay.
const DefaultKeep = 3

// MigrationError is a migration that failed; the store stays at Reached.
type MigrationError struct {
	From, Reached uint32
	Step          string
	Backup        string
	Err           error
}

func (e *MigrationError) Error() string {
	if e.Step == "" {
		return fmt.Sprintf("the store migration from version %d failed: %v; the store is unchanged at version %d", e.From, e.Err, e.Reached)
	}
	return fmt.Sprintf("the store migration from version %d failed at %s: %v; the store is at version %d, and a copy from before the migration is at %s", e.From, e.Step, e.Err, e.Reached, e.Backup)
}

func (e *MigrationError) Unwrap() error { return e.Err }

// freeSpace is freeBytes, which tests replace.
var freeSpace = freeBytes

// ErrDamaged is a store that fails SQLite's integrity checks.
var ErrDamaged = errors.New("the store is damaged")

// ErrNoSpace is too little free space for the backup a migration takes.
var ErrNoSpace = errors.New("not enough free space for the store's backup")

// Migrate takes the store at path to SchemaVersion: it checks the store,
// copies it into o.Backups, runs each pending migration in its own
// transaction, checks the result and prunes old backups. The caller holds
// the store's lock (LockStore). A store with nothing pending is left alone.
func Migrate(ctx context.Context, path string, o MigrateOptions) (Plan, error) {
	return current.migrate(ctx, path, o)
}

func (set schemaSet) migrate(ctx context.Context, path string, o MigrateOptions) (Plan, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	p, err := set.plan(path, o.Backups, now())
	if err != nil || !p.Pending() {
		return p, err
	}
	if o.Backup != "" {
		p.Backup = o.Backup
	}
	keep := o.Keep
	if keep == 0 {
		keep = DefaultKeep
	}
	warn := func(err error) {
		if err != nil && o.Warn != nil {
			o.Warn(err)
		}
	}
	fail := func(err error) (Plan, error) {
		return p, &MigrationError{From: p.From, Reached: p.From, Err: err}
	}
	if p.Short() {
		return fail(fmt.Errorf("%w: it needs about %s free in %s, and %s is free", ErrNoSpace, Megabytes(2*p.Size), o.Backups, Megabytes(p.Free)))
	}
	c, err := connector(path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return fail(err)
	}
	db := sql.OpenDB(c)
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return fail(err)
	}
	defer conn.Close()
	if err := check(ctx, conn, "quick_check"); err != nil {
		return fail(err)
	}
	if err := backupTo(ctx, conn, p.Backup); err != nil {
		return fail(err)
	}
	defer func() { warn(prune(o.Backups, keep, now(), p.Backup)) }()
	reached := p.From
	for _, m := range set.pending(p.From) {
		start := time.Now()
		if err := set.step(ctx, conn, m); err != nil {
			return p, &MigrationError{From: p.From, Reached: reached, Step: m.label(), Backup: p.Backup, Err: err}
		}
		reached = m.to
		if o.Step != nil {
			o.Step(m.label(), time.Since(start))
		}
	}
	if err := check(ctx, conn, "integrity_check"); err != nil {
		return p, &MigrationError{From: p.From, Reached: reached, Step: "the final check", Backup: p.Backup, Err: err}
	}
	var busy, frames, done int
	if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &done); err != nil {
		warn(err)
	} else if busy != 0 {
		warn(errors.New("a reader kept the WAL from being checkpointed after the migration; the store's header shows the new version once it closes"))
	}
	return p, nil
}

// step runs m on conn. Foreign key enforcement can change only outside a
// transaction, so it is turned off before BEGIN and back on after COMMIT or
// ROLLBACK, on the same connection.
func (set schemaSet) step(ctx context.Context, conn *sql.Conn, m migration) (err error) {
	var before int
	if m.foreignKeysOff {
		if before, err = violations(ctx, conn); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		defer func() {
			if _, on := conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`); on != nil && err == nil {
				err = on
			}
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v uint32
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v != m.to-1 {
		return fmt.Errorf("the store is at version %d, not %d", v, m.to-1)
	}
	if err := m.up(ctx, tx); err != nil {
		return err
	}
	if m.foreignKeysOff {
		after, err := violations(ctx, tx)
		if err != nil {
			return err
		}
		if after > before {
			return fmt.Errorf("it leaves %d rows that break a foreign key", after-before)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.to)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		compatibleKey, strconv.FormatUint(uint64(set.compatibleAt(m.to)), 10)); err != nil {
		return err
	}
	return tx.Commit()
}

type rowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func violations(ctx context.Context, q rowsQueryer) (int, error) {
	rows, err := q.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

// check runs PRAGMA quick_check or integrity_check and fails on anything but
// ok, with the first problems it names.
func check(ctx context.Context, q rowsQueryer, pragma string) error {
	rows, err := q.QueryContext(ctx, `PRAGMA `+pragma)
	if err != nil {
		return err
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		found = append(found, line)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(found) == 1 && found[0] == "ok" {
		return nil
	}
	if len(found) > 3 {
		found = append(found[:3], fmt.Sprintf("and %d more", len(found)-3))
	}
	return fmt.Errorf("%w: it fails its %s: %s", ErrDamaged, strings.ReplaceAll(pragma, "_", " "), strings.Join(found, "; "))
}

// backupTo writes a consistent copy of the store on conn to dst, through a
// temporary file so an interrupted copy never takes the name.
func backupTo(ctx context.Context, conn *sql.Conn, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backing up the store to %s: %w", dst, err)
	}
	if err := os.Chmod(tmp, storeMode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// Megabytes is n bytes as whole megabytes, rounded up.
func Megabytes(n int64) string {
	return fmt.Sprintf("%d MB", (n+999_999)/1_000_000)
}
