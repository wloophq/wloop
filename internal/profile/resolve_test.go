package profile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestAUserFileReplacesTheBundledOneOfItsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "intents", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "intents", "review", "instructions.md.tmpl"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	layers := []Layer{{Name: "bundled", Source: "embedded"}, {Name: "user", Source: dir, Dir: dir}}
	r, err := Resolve(layers, "intents/review/instructions.md.tmpl")
	if err != nil || string(r.Body) != "mine" || r.Layer.Name != "user" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = Resolve(layers, "intents/review/intent.toml")
	if err != nil || r.Layer.Name != "bundled" || r.Path != "default/intents/review/intent.toml" || len(r.Body) == 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := Resolve(layers, "prompts/absent.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("absent: %v", err)
	}
	if _, err := Resolve(layers, "../etc/passwd"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("outside: %v", err)
	}
}

func TestFilesListsADirectoryThroughTheLayers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "partials"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"record.md.tmpl": "mine", ".hidden": "x"} {
		if err := os.WriteFile(filepath.Join(dir, "partials", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Files([]Layer{{Name: "bundled"}, {Name: "user", Dir: dir}}, PartialsDir)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := got["partials/record.md.tmpl"]; !ok || string(r.Body) != "mine" || len(got) < 1 {
		t.Fatalf("%v", got)
	}
	if _, ok := got["partials/.hidden"]; ok {
		t.Fatal("a dotfile resolved")
	}
	if got, err := Files([]Layer{{Name: "bundled"}}, "nothing-here"); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}
