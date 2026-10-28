package intent

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// NoticesFile is the profile file that words what the loop tells a session.
const NoticesFile = "notices.toml"

// loadNotices reads notices.toml from bundled and the user's layers, by notice name, as templates over the
// fields of its builtin.Notices row: a higher layer's key replaces a lower one's, and a user's text that fails
// keeps the lower one.
func loadNotices(layers []profile.Layer, bundled fs.FS) (map[string]string, Problems) {
	out := map[string]string{}
	if bundled == nil {
		return out, nil
	}
	src, err := fs.ReadFile(bundled, NoticesFile)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, Problems{{File: NoticesFile, Msg: err.Error()}}
	}
	problems := mergeNotices(out, NoticesFile, src)
	for _, l := range layers {
		if l.Bundled() {
			continue
		}
		r, err := profile.ReadFile(l, NoticesFile)
		file, src := r.Path, r.Body
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: file, Msg: err.Error()})
			continue
		}
		problems = append(problems, mergeNotices(out, file, src)...)
	}
	for _, n := range builtin.Notices {
		if _, ok := out[n.Name]; !ok {
			problems = append(problems, Problem{File: NoticesFile, Field: n.Name, Msg: "is missing: every notice has a text"})
		}
	}
	return out, problems
}

func mergeNotices(out map[string]string, file string, src []byte) Problems {
	m, problems, _, complete := decode[map[string]string](file, src)
	if !complete {
		return problems
	}
	var names []string
	for _, n := range builtin.Notices {
		names = append(names, n.Name)
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	for _, name := range slices.Sorted(maps.Keys(m)) {
		row, ok := builtin.NoticeOf(name)
		if !ok {
			s.add(name, "names no notice%s", expr.DidYouMean(name, names))
			continue
		}
		v := strings.TrimSpace(m[name])
		if v == "" {
			s.add(name, "may not be empty")
			continue
		}
		if s.toolText(name, v, row.Fields) {
			out[name] = v
		}
	}
	s.addGrammar(grammar(builtin.SurfaceNotices, src))
	return s.problems
}

// BundledNotices are the notices' texts of bundled alone.
func BundledNotices(bundled fs.FS) (map[string]string, Problems) {
	return loadNotices(nil, bundled)
}
