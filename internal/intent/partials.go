package intent

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/profile"
)

// partialFiles are the user's partials: the template files under partials/
// of each directory layer, by their path relative to the profile, a higher
// layer's replacing a lower one's.
func partialFiles(layers []profile.Layer) (map[string]profile.Resolved, Problems) {
	var dirs []profile.Layer
	for _, l := range layers {
		if l.Dir != "" {
			dirs = append(dirs, l)
		}
	}
	files, err := profile.Files(dirs, profile.PartialsDir)
	if err != nil {
		return nil, Problems{{File: profile.PartialsDir, Msg: err.Error()}}
	}
	maps.DeleteFunc(files, func(rel string, _ profile.Resolved) bool { return path.Ext(rel) != ".tmpl" })
	return files, nil
}

// withPartials is base with the user's partials parsed over the bundled
// ones, each under its path on disk, so that a problem a partial causes
// names its file; reverted are the user's partials left out, and a file
// that does not parse is left out with its problem.
func withPartials(base *template.Template, layers []profile.Layer, reverted revertedPartials) (*template.Template, Problems) {
	files, problems := partialFiles(layers)
	if len(files) == 0 || base == nil {
		return base, problems
	}
	set, err := base.Clone()
	if err != nil {
		return base, append(problems, Problem{File: profile.PartialsDir, Msg: err.Error()})
	}
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		f := files[rel]
		if _, skip := reverted[f.Path]; skip {
			continue
		}
		if _, err := template.Must(set.Clone()).New(f.Path).Parse(string(f.Body)); err != nil {
			line, msg := templateMsg(f.Path, err)
			reverted[f.Path] = Problems{{File: f.Path, Line: line, Col: 1, Msg: msg + "; the bundled partials load instead"}}
			continue
		}
		if _, err := set.New(f.Path).Parse(string(f.Body)); err != nil {
			return base, append(problems, Problem{File: f.Path, Msg: err.Error()})
		}
	}
	return set, problems
}

// revertedPartials are the user's partials left out of a load, by their
// path on disk, with the problems that broke them.
type revertedPartials map[string]Problems

func (r revertedPartials) problems() Problems {
	var out Problems
	for _, p := range slices.Sorted(maps.Keys(r)) {
		out = append(out, r[p]...)
	}
	return out
}

// breaking adds to r the user's partials a problem of problems names, and
// says whether it added one.
func (r revertedPartials) breaking(layers []profile.Layer, problems Problems) bool {
	files, _ := partialFiles(layers)
	added := false
	for _, f := range files {
		if _, done := r[f.Path]; done {
			continue
		}
		for _, p := range problems {
			if p.File == f.Path || strings.HasPrefix(p.Msg, f.Path+":") {
				r[f.Path] = append(r[f.Path], Problem{File: p.File, Line: p.Line, Col: p.Col, Field: p.Field,
					Msg: fmt.Sprintf("%s; the bundled partials load instead", p.Msg)})
				added = true
			}
		}
	}
	return added
}
