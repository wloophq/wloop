package sqlitetest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
)

var empty struct {
	once sync.Once
	raw  []byte
	err  error
}

// Open is sqlite.Open, except that a store absent at path starts as a copy of an
// empty store made once per test binary: creating the schema costs a tenth of a
// second under -race, and a package with hundreds of stores spends minutes on it.
func Open(path string, log sqlite.Noter) (*sqlite.DB, error) {
	empty.once.Do(func() {
		dir, err := os.MkdirTemp("", "sqlitetest-")
		if err != nil {
			empty.err = err
			return
		}
		defer os.RemoveAll(dir)
		src := filepath.Join(dir, "empty.db")
		db, err := sqlite.Open(src, nil)
		if err != nil {
			empty.err = err
			return
		}
		if empty.err = db.Close(); empty.err != nil {
			return
		}
		empty.raw, empty.err = os.ReadFile(src)
	})
	if empty.err != nil {
		return nil, empty.err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := place(path); err != nil {
		return nil, err
	}
	return sqlite.Open(path, log)
}

// place links a whole copy of the empty store at path unless a file is there,
// so a daemon opening path at the same time never sees a partial copy.
func place(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sqlitetest-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(empty.raw)
	if err := errors.Join(err, tmp.Chmod(0o600), tmp.Close()); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}
