package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editRig is a config file holding raw, and the backups and writes an edit of it made.
type editRig struct {
	file    string
	backups [][]byte
}

func newEditRig(t *testing.T, raw string) *editRig {
	t.Helper()
	r := &editRig{file: filepath.Join(t.TempDir(), "config.toml")}
	if raw != "" {
		if err := os.WriteFile(r.file, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *editRig) edit(path []string, v any, load func([]byte) error) (Edited, error) {
	return EditFile(r.file, FileEdit{Path: path, Edit: func(d *Doc, p []string) error { return d.Set(p, v) }, Load: load,
		Backup: func(raw []byte) (string, error) { r.backups = append(r.backups, raw); return "backup", nil },
		Write:  func(raw []byte) error { return os.WriteFile(r.file, raw, 0o644) }})
}

func (r *editRig) read(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(r.file)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEditFileBacksUpAndValidates(t *testing.T) {
	t.Parallel()
	const before = "# mine\nprofile = \"p\"\n"
	r := newEditRig(t, before)
	refuse := func(raw []byte) error {
		if strings.Contains(string(raw), "enabled = false") {
			return errors.New("does not load")
		}
		return nil
	}
	if _, err := r.edit([]string{"workflows", "wf", "enabled"}, false, refuse); err == nil {
		t.Fatal("an edit that does not load was written")
	}
	if got := r.read(t); got != before || len(r.backups) != 0 {
		t.Fatalf("after a refused edit: file %q, %d backups", got, len(r.backups))
	}
	e, err := r.edit([]string{"workflows", "wf", "enabled"}, false, nil)
	if err != nil || !e.Changed || e.Backup != "backup" {
		t.Fatalf("%+v %v", e, err)
	}
	if got := r.read(t); !strings.HasPrefix(got, "# mine\n") || !strings.Contains(got, "enabled = false") {
		t.Errorf("file %q", got)
	}
	if len(r.backups) != 1 || string(r.backups[0]) != before {
		t.Errorf("backups %q", r.backups)
	}
	again, err := r.edit([]string{"workflows", "wf", "enabled"}, false, nil)
	if err != nil || again.Changed || len(r.backups) != 1 {
		t.Errorf("an edit that changes nothing: %+v %v, %d backups", again, err, len(r.backups))
	}
}

func TestEditFileCreatesAMissingFileWithoutABackup(t *testing.T) {
	t.Parallel()
	r := newEditRig(t, "")
	e, err := r.edit([]string{"workflows", "wf", "enabled"}, false, nil)
	if err != nil || !e.Changed || e.Backup != "" || len(r.backups) != 0 {
		t.Fatalf("%+v %v, %d backups", e, err, len(r.backups))
	}
	if !strings.Contains(r.read(t), "enabled = false") {
		t.Errorf("file %q", r.read(t))
	}
}
