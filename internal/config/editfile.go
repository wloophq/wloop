package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// FileEdit is one edit of a config file.
type FileEdit struct {
	// Path is the key the edit changes.
	Path []string
	// Edit changes the document at Path.
	Edit func(d *Doc, path []string) error
	// Load says why a file's bytes do not load; nil checks nothing.
	Load func(raw []byte) error
	// Backup keeps the file's previous bytes, naming where.
	Backup func(raw []byte) (string, error)
	// Write replaces the file with raw.
	Write func(raw []byte) error
}

// Edited is what EditFile did.
type Edited struct {
	// Doc is the edited document.
	Doc *Doc
	// Changed says the file was written.
	Changed bool
	// Backup is where the previous file was kept; "" when there was none.
	Backup string
	// Still is why the written file does not load, when the file did not load before the edit either and the
	// edit removed Path.
	Still error
}

// EditFile edits the config file at file: it applies e.Edit, refuses a result that does not load (unless the
// file did not load before and the edit removed the key), keeps the previous file through e.Backup and
// writes the result through e.Write. A missing file reads as empty and is not backed up.
func EditFile(file string, e FileEdit) (Edited, error) {
	raw, err := os.ReadFile(file)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Edited{}, err
	}
	d, err := ParseDoc(raw)
	if err != nil {
		return Edited{}, fmt.Errorf("%s: %w", file, err)
	}
	out := Edited{Doc: d}
	if err := e.Edit(d, e.Path); err != nil {
		return out, err
	}
	next := d.Bytes()
	if bytes.Equal(next, raw) {
		return out, nil
	}
	if e.Load != nil {
		if err := e.Load(next); err != nil {
			if e.Load(raw) == nil || d.Get(e.Path...) != nil {
				return out, err
			}
			out.Still = err
		}
	}
	if exists {
		if out.Backup, err = e.Backup(raw); err != nil {
			return out, err
		}
	}
	if err := e.Write(next); err != nil {
		return out, err
	}
	out.Changed = true
	return out, nil
}
