package intent

import (
	"io/fs"
	"path"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/profiles"
)

func TestBundledProfileLoadsClean(t *testing.T) {
	sub, err := fs.Sub(profiles.Default, "default")
	if err != nil {
		t.Fatal(err)
	}
	check := func(file string, surface builtin.Surface) {
		b, err := fs.ReadFile(sub, file)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := toml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, p := range builtin.Check(surface, doc) {
			t.Errorf("%s: %s: %s", file, p.Path, p.Msg)
		}
		if surface != builtin.SurfaceWorkflow {
			return
		}
		steps, _ := doc["step"].([]any)
		for i, raw := range steps {
			args := raw.(map[string]any)
			use, _ := args["use"].(string)
			delete(args, "use")
			for _, p := range builtin.CheckStep(use, args) {
				t.Errorf("%s: step[%d].%s: %s", file, i, p.Path, p.Msg)
			}
		}
	}
	for _, f := range []struct {
		file    string
		surface builtin.Surface
	}{{ActionsFile, builtin.SurfaceActions}, {DefaultsFile, builtin.SurfaceDefaults}, {"labels.toml", builtin.SurfaceLabels}, {GTDFile, builtin.SurfaceGTD}, {KeysFile, builtin.SurfaceKeys}} {
		check(f.file, f.surface)
	}
	for dir, surface := range map[string]builtin.Surface{"intents": builtin.SurfaceIntent, "workflows": builtin.SurfaceWorkflow} {
		entries, err := fs.ReadDir(sub, dir)
		if err != nil || len(entries) == 0 {
			t.Fatalf("%s: %v", dir, err)
		}
		manifest := map[string]string{"intents": "intent.toml", "workflows": "workflow.toml"}[dir]
		for _, e := range entries {
			if e.IsDir() {
				check(path.Join(dir, e.Name(), manifest), surface)
			}
		}
	}
}
