package dossier

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Archive writes dir, its repository included, to tarball; symlinks are left out.
func (f Files) Archive(ctx context.Context, dir, tarball string) error {
	if err := f.inside(dir); err != nil {
		return err
	}
	if err := f.inside(tarball); err != nil {
		return err
	}
	if !strings.HasSuffix(tarball, ".tar.gz") {
		return fmt.Errorf("%s is not a .tar.gz", tarball)
	}
	if fi, err := os.Stat(dir); err != nil {
		return err
	} else if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if err := os.MkdirAll(filepath.Dir(tarball), dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(tarball), ".archive-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return err
	}
	if err := writeTar(ctx, tmp, dir); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), tarball)
}

func writeTar(ctx context.Context, w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == dir || !(d.IsDir() || d.Type().IsRegular()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name, h.Uname, h.Gname, h.Uid, h.Gid = filepath.ToSlash(rel), "", "", 0, 0
		if d.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		src, err := os.Open(p) //nolint:gosec // G122: walks the dossier directory, which only this user can write
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, src)
		return errors.Join(err, src.Close())
	})
	return errors.Join(err, tw.Close(), gz.Close())
}

// Restore replaces dir with the content of tarball; an entry that is not a plain file or
// directory, or that leaves dir, fails the restore and leaves dir as it was.
func (f Files) Restore(ctx context.Context, tarball, dir string) error {
	if err := f.inside(dir); err != nil {
		return err
	}
	if err := f.inside(tarball); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), dirMode); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, dirMode); err != nil {
		return err
	}
	if err := extract(ctx, tarball, tmp); err != nil {
		return fmt.Errorf("restoring %s: %w", tarball, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

func extract(ctx context.Context, tarball, into string) error {
	src, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer src.Close()
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(strings.TrimSuffix(h.Name, "/"))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("the entry %q leaves the dossier", h.Name)
		}
		target := filepath.Join(into, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, dirMode); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), dirMode); err != nil {
				return err
			}
			if err := writeFile(target, tr, os.FileMode(h.Mode&0o777)&^0o077|0o400); err != nil {
				return err
			}
		default:
			return fmt.Errorf("the entry %q is not a file or a directory", h.Name)
		}
	}
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, r)
	return errors.Join(err, dst.Close())
}
