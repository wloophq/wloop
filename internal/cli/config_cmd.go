package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/backup"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/frontmatter"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

var configUsage = "usage: " + brand.Command + " config get <key>\n" +
	"       " + brand.Command + " config set <key> <value>\n" +
	"       " + brand.Command + " config unset <key>"

func runConfig(e *Env, args []string) error {
	switch {
	case len(args) == 2 && args[0] == "get":
		return runConfigGet(e, args[1])
	case len(args) == 3 && args[0] == "set":
		return runConfigEdit(e, args[1], func(d *config.Doc, path []string) error {
			if err := e.knownWorkflow(path); err != nil {
				return err
			}
			return d.Put(path, config.ParseValue(args[2]))
		})
	case len(args) == 2 && args[0] == "unset":
		return runConfigEdit(e, args[1], (*config.Doc).Unset)
	}
	return errors.New(configUsage)
}

// knownWorkflow refuses a workflows.<name> key whose name is none of the loaded profile's workflows, nor one
// of a disabled plugin's layer, which no setting would read.
func (e *Env) knownWorkflow(path []string) error {
	if len(path) < 2 || path[0] != config.KeyOf("Workflows") {
		return nil
	}
	l := e.loadedIntents()
	var names []string
	for _, w := range l.Workflows {
		names = append(names, w.Name)
	}
	names = append(names, slices.Collect(maps.Keys(l.BrokenWorkflows))...)
	for _, layer := range profile.EveryPlugin(e.currentConfig()) {
		entries, _ := profile.ReadDir(layer, "workflows")
		for _, d := range entries {
			if d.IsDir() {
				names = append(names, d.Name())
			}
		}
	}
	if slices.Contains(names, path[1]) {
		return nil
	}
	return fmt.Errorf("%s: the profile has no workflow %q%s; %s doctor lists its workflows",
		config.Key(path...), path[1], expr.DidYouMean(path[1], names), brand.Command)
}

// readsNothing says whether path is one of the keys of the config file raw that no setting reads.
func readsNothing(raw []byte, path []string) bool {
	var unknown *config.UnknownKeysError
	_, err := config.LoadBytes("", raw, Defaults())
	return errors.As(err, &unknown) && slices.Contains(unknown.Keys, config.Key(path...))
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

// editConfig edits key path of the config file through config.EditFile: a result that does not load with
// the defaults is refused, the previous file is backed up beside it and the new one written atomically.
// Edits through one Env take turns, so two at once both land.
func (e *Env) editConfig(path []string, edit func(*config.Doc, []string) error) (config.Edited, error) {
	if e.configMu != nil {
		e.configMu.Lock()
		defer e.configMu.Unlock()
	}
	return config.EditFile(e.ConfigPath, config.FileEdit{Path: path, Edit: edit,
		Load: func(raw []byte) error {
			_, err := config.LoadBytes(e.ConfigPath, raw, Defaults())
			return err
		},
		Backup: func(raw []byte) (string, error) {
			return backup.File(e.ConfigPath, raw, e.Now().UTC().Format("20060102-150405"))
		},
		Write: func(raw []byte) error { return writeAtomic(e.ConfigPath, raw) }})
}

// setConfig sets key path of the config file to v, then makes the serve's config holder read it, so the keys
// that apply live apply now.
func (e *Env) setConfig(path []string, v any) error {
	if _, err := e.editConfig(path, func(d *config.Doc, p []string) error { return d.Set(p, v) }); err != nil {
		return err
	}
	if e.cfg != nil {
		e.cfg.Reload()
	}
	return nil
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
	raw, _ := os.ReadFile(e.ConfigPath)
	done, err := e.editConfig(path, edit)
	if err != nil {
		return err
	}
	d, still := done.Doc, done.Still
	key = config.Key(path...)
	if !done.Changed {
		if v := d.Get(path...); v != nil {
			enc, _ := frontmatter.Value(v)
			_, err = fmt.Fprintf(e.Out, "%s is already %s in %s.\n", key, enc, file)
		} else {
			_, err = fmt.Fprintf(e.Out, "%s is not set in %s.\n", key, file)
		}
		return err
	}
	if done.Backup != "" {
		fmt.Fprintf(e.Out, "Kept the previous config in %s.\n", tilde(done.Backup, e.Home))
	}
	if v := d.Get(path...); v != nil {
		enc, _ := frontmatter.Value(v)
		fmt.Fprintf(e.Out, "Set %s = %s in %s.\n", key, enc, file)
	} else if readsNothing(raw, path) || e.knownWorkflow(path) != nil {
		fmt.Fprintf(e.Out, "Removed %s from %s.\n", key, file)
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
