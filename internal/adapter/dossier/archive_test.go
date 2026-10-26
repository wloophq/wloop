package dossier_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeTarball(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("planted")
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRestoreRefusesAnEntryLeavingTheDossier(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../escaped", "sources/../../escaped", "/escaped", "..", ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			base := filepath.Join(e.state, "dossiers", "github", "owner")
			tarball := filepath.Join(e.state, "dossiers", ".archive", "hostile.tar.gz")
			writeTarball(t, tarball, name)
			dir := filepath.Join(base, "repo")
			if err := e.files.Restore(context.Background(), tarball, dir); err == nil {
				t.Fatalf("restoring an entry %q succeeded", name)
			}
			if _, err := os.Stat(filepath.Join(base, "escaped")); err == nil {
				t.Fatalf("the entry %q was written outside the dossier", name)
			}
			if _, err := os.Stat(dir); err == nil {
				t.Fatalf("the restore of %q left a dossier behind", name)
			}
		})
	}
}
