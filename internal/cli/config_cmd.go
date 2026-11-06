package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/wloophq/wloop/internal/backup"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/frontmatter"
)

var configUsage = "usage: " + brand.Command + " config get <key>\n" +
	"       " + brand.Command + " config set <key> <value>\n" +
	"       " + brand.Command + " config unset <key>"

func runConfig(e *Env, args []string) error {
	switch {
	case len(args) == 2 && args[0] == "get":
		return runConfigGet(e, args[1])
	case len(args) == 3 && args[0] == "set":
		return runConfigEdit(e, args[1], func(d *config.Doc, path []string) error { return d.Put(path, config.ParseValue(args[2])) })
	case len(args) == 2 && args[0] == "unset":
		return runConfigEdit(e, args[1], (*config.Doc).Unset)
	}
	return errors.New(configUsage)
}

// runConfigGet prints a key of the effective configuration, so skills read
// the same values the daemon uses.
func runConfigGet(e *Env, key string) error {
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	for part := range strings.SplitSeq(key, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("no config key %q", key)
		}
		if v, ok = m[part]; !ok {
			return fmt.Errorf("no config key %q", key)
		}
	}
	if s, ok := v.(string); ok {
		_, err := fmt.Fprintln(e.Out, s)
		return err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(e.Out, string(out))
	return err
}

// runConfigEdit applies edit to key in the config file and writes the
// result, after a backup of the file, when the result loads. A file that
// does not load may still lose a key, so unset can remove the keys it names.
func runConfigEdit(e *Env, key string, edit func(*config.Doc, []string) error) error {
	path, err := config.ParseKey(key)
	if err != nil {
		return err
	}
	file := tilde(e.ConfigPath, e.Home)
	raw, err := os.ReadFile(e.ConfigPath)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	d, err := config.ParseDoc(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", e.ConfigPath, err)
	}
	if err := edit(d, path); err != nil {
		return err
	}
	next := d.Bytes()
	key = config.Key(path...)
	if bytes.Equal(next, raw) {
		if v := d.Get(path...); v != nil {
			enc, _ := frontmatter.Value(v)
			_, err = fmt.Fprintf(e.Out, "%s is already %s in %s.\n", key, enc, file)
		} else {
			_, err = fmt.Fprintf(e.Out, "%s is not set in %s.\n", key, file)
		}
		return err
	}
	var still error
	if _, err := config.LoadBytes(e.ConfigPath, next, Defaults()); err != nil {
		_, was := config.LoadBytes(e.ConfigPath, raw, Defaults())
		if was == nil || d.Get(path...) != nil {
			return err
		}
		still = err
	}
	if exists {
		name, err := backup.File(e.ConfigPath, raw, e.Now().UTC().Format("20060102-150405"))
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "Kept the previous config in %s.\n", tilde(name, e.Home))
	}
	if err := writeAtomic(e.ConfigPath, next); err != nil {
		return err
	}
	if v := d.Get(path...); v != nil {
		enc, _ := frontmatter.Value(v)
		fmt.Fprintf(e.Out, "Set %s = %s in %s.\n", key, enc, file)
	} else {
		fmt.Fprintf(e.Out, "Removed %s from %s; its default applies.\n", key, file)
	}
	if still != nil {
		fmt.Fprintf(e.Out, "%s still does not load: %v\n", file, still)
	}
	if len(path) == 1 && path[0] == config.KeyOf("Profile") {
		_, err = fmt.Fprintln(e.Out, "The daemon applies it live.")
	} else {
		_, err = fmt.Fprintln(e.Out, "Restart the daemon to apply it.")
	}
	return err
}
