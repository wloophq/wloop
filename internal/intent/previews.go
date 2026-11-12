package intent

import (
	"errors"
	"io/fs"
	"text/template"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// loadPreviews reads the summary-line template of each draft kind the merged plugins declare one for, from
// its plugin's profile layers, by provider and kind: a higher layer's file replaces a lower one's. A template
// that does not parse is a problem and is left out.
func loadPreviews(layers []profile.Layer, r layerReader) (map[string]map[string]string, Problems) {
	out := map[string]map[string]string{}
	var problems Problems
	for _, l := range layers {
		id := l.PluginID()
		if id == "" {
			continue
		}
		for _, k := range builtin.DraftKinds.Kinds() {
			if k.Provider != id || k.Preview == "" {
				continue
			}
			res, err := r.read(l, k.Preview)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				problems = append(problems, Problem{File: res.Path, Msg: err.Error()})
				continue
			}
			if _, err := template.New(k.Name).Parse(string(res.Body)); err != nil {
				problems = append(problems, Problem{File: res.Path, Msg: err.Error()})
				continue
			}
			if out[id] == nil {
				out[id] = map[string]string{}
			}
			out[id][k.Name] = string(res.Body)
		}
	}
	return out, problems
}
