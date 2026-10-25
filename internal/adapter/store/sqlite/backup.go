package sqlite

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// ErrStoreBusy is a store whose lock another process holds.
var ErrStoreBusy = errors.New("another process has the store open")

// lockPath is the lock file beside the store at path.
func lockPath(path string) string { return filepath.Join(filepath.Dir(path), "store.lock") }

// Backup is a copy of the store in the backups directory.
type Backup struct {
	Path    string
	Version uint32
	Taken   time.Time
	// PreRestore is the store as it was before a restore replaced it.
	PreRestore bool
	Size       int64
	// nth orders backups taken in the same second: 1, then -2, -3...
	nth int
}

const stampLayout = "20060102T150405Z"

// PreRestoreDays is how long the copies Restore keeps of a replaced store
// stay.
const PreRestoreDays = 30

var backupFile = regexp.MustCompile(`^store-v(\d+)-(\d{8}T\d{6}Z)(?:-(\d+))?(\.pre-restore)?\.db$`)

// backupName is a free name in dir for a backup of a store at version v.
func backupName(dir string, v uint32, now time.Time, preRestore bool) (string, error) {
	suffix := ""
	if preRestore {
		suffix = ".pre-restore"
	}
	base := fmt.Sprintf("store-v%d-%s", v, now.UTC().Format(stampLayout))
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name += "-" + strconv.Itoa(n)
		}
		path := filepath.Join(dir, name+suffix+".db")
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
}

// Backups lists the backups in dir, newest first; an absent dir has none.
func Backups(dir string) ([]Backup, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		m := backupFile.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		v, err := strconv.ParseUint(m[1], 10, 32)
		if err != nil {
			continue
		}
		taken, err := time.Parse(stampLayout, m[2])
		if err != nil {
			continue
		}
		b := Backup{Path: filepath.Join(dir, e.Name()), Version: uint32(v), Taken: taken, PreRestore: m[4] != "", nth: 1}
		if m[3] != "" {
			b.nth, _ = strconv.Atoi(m[3])
		}
		if fi, err := e.Info(); err == nil {
			b.Size = fi.Size()
		}
		out = append(out, b)
	}
	slices.SortStableFunc(out, func(a, b Backup) int {
		if c := b.Taken.Compare(a.Taken); c != 0 {
			return c
		}
		return cmp.Compare(b.nth, a.nth)
	})
	return out, nil
}

// removeFile is os.Remove, which tests replace.
var removeFile = os.Remove

// prune keeps the keep newest pre-migration backups in dir and the
// pre-restore copies younger than PreRestoreDays; keepPath always stays.
func prune(dir string, keep int, now time.Time, keepPath string) error {
	all, err := Backups(dir)
	if err != nil {
		return err
	}
	var errs []error
	kept := 0
	for _, b := range all {
		stays := b.Path == keepPath
		switch {
		case b.PreRestore:
			stays = stays || now.Sub(b.Taken) < PreRestoreDays*24*time.Hour
		case kept < keep:
			stays = true
		}
		if !b.PreRestore && stays {
			kept++
		}
		if stays {
			continue
		}
		for _, f := range []string{b.Path, b.Path + "-wal", b.Path + "-shm"} {
			if err := removeFile(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Restorable is the newest backup in all, newest first as Backups lists
// them, that this release opens or migrates; pre-restore copies are left
// for an explicit choice.
func Restorable(all []Backup) (Backup, bool) {
	for _, b := range all {
		if !b.PreRestore && b.Version >= 1 && b.Version <= SchemaVersion {
			return b, true
		}
	}
	return Backup{}, false
}

// Restored is what Restore did.
type Restored struct {
	From Status
	// Kept is the copy of the store Restore replaced; empty when there was
	// none.
	Kept string
}

// Restore replaces the store at path with the backup at from, after copying
// the store into the backups directory as a pre-restore copy. The caller
// holds the store's lock. A backup older than SchemaVersion stays older:
// Migrate takes it forward.
func Restore(ctx context.Context, path, from, backups string, now time.Time) (Restored, error) {
	return current.restore(ctx, path, from, backups, now)
}

func (set schemaSet) restore(ctx context.Context, path, from, backups string, now time.Time) (Restored, error) {
	s, err := set.inspect(from)
	if err != nil {
		return Restored{}, err
	}
	if s.Empty {
		return Restored{}, fmt.Errorf("%s is not a store", from)
	}
	switch s.stateFor(set.version) {
	case StateNewer, StateUnknown:
		return Restored{}, SchemaError{from, s, set.version}
	}
	// Not query_only: SQLite refuses VACUUM INTO on such a connection.
	src, err := handle(from+"?_pragma=busy_timeout(5000)", 1)
	if err != nil {
		return Restored{}, err
	}
	defer src.Close()
	if err := check(ctx, src, "quick_check"); err != nil {
		return Restored{}, fmt.Errorf("%s: %w", from, err)
	}
	out := Restored{From: s}
	var need int64
	for _, f := range []string{from, from + "-wal", path, path + "-wal"} {
		if fi, err := os.Stat(f); err == nil {
			need += fi.Size()
		}
	}
	if free := freeSpace(backups); free >= 0 && free < need {
		return out, fmt.Errorf("%w: it needs about %s free in %s, and %s is free", ErrNoSpace, Megabytes(need), backups, Megabytes(free))
	}
	// An unreadable store is still kept aside: it is the store a restore is for.
	if cur, err := set.inspect(path); err != nil || !cur.Empty {
		if out.Kept, err = backupName(backups, cur.Version, now, true); err != nil {
			return out, err
		}
		if err := keepAside(ctx, path, out.Kept); err != nil {
			return out, err
		}
	}
	// A copy through SQLite carries what the source still holds in its WAL.
	tmp := path + ".restore"
	conn, err := src.Conn(ctx)
	if err != nil {
		return out, err
	}
	err = backupTo(ctx, conn, tmp)
	conn.Close()
	if err != nil {
		return out, err
	}
	for _, f := range []string{path + "-wal", path + "-shm"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(tmp)
			return out, err
		}
	}
	return out, os.Rename(tmp, path)
}

// keepAside copies the store at path to dst; a store too damaged to copy
// through SQLite is copied file by file with its WAL. The store stays in
// place either way, so a restore that fails after it loses nothing.
func keepAside(ctx context.Context, path, dst string) error {
	db, err := handle(path+"?_pragma=busy_timeout(5000)", 1)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err == nil {
		err = backupTo(ctx, conn, dst)
		conn.Close()
	}
	if err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := copyFile(path, dst); err != nil {
		return err
	}
	if err := copyFile(path+"-wal", dst+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, storeMode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
