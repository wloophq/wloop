package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
)

func dir(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range files {
		f := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func bundled() Layer { return Layer{Name: "bundled", Source: "embedded"} }

func texts(g Guide) string {
	var out []string
	for _, p := range g.Parts {
		out = append(out, strings.TrimSpace(p.Text))
	}
	return strings.Join(out, "|")
}

func TestMissingProfileDirectory(t *testing.T) {
	_, err := Load([]Layer{bundled(), {Name: "user", Dir: "/nope"}}, testIntents, nil)
	var m MissingError
	if !errors.As(err, &m) || m.Dir != "/nope" {
		t.Fatalf("%v", err)
	}
}

func TestFrontMatterRejectsUnknownKeysAndBadValues(t *testing.T) {
	for _, fm := range []string{"extnd = true", "override = \"yes\"", "delegates = \"/code-review\"", "[delegates]\nclaude = \"/x\"", "[delegates]\nclaude = [1]", "override = true\noverride = false", "override: true"} {
		d := dir(t, map[string]string{"intent=review.md": "+++\n" + fm + "\n+++\nX"})
		_, err := Load([]Layer{bundled(), user(d)}, testIntents, nil)
		var f FrontMatterError
		if !errors.As(err, &f) || f.File != filepath.Join(d, "intent=review.md") {
			t.Errorf("%q: %v", fm, err)
		}
	}
}

func TestABlockThatMatchesNothingIsStillValidated(t *testing.T) {
	d := dir(t, map[string]string{"organization=other/intent=review.md": "+++\nextnd = true\n+++\nU"})
	var f FrontMatterError
	if _, err := Load([]Layer{bundled(), user(d)}, testIntents, nil); !errors.As(err, &f) {
		t.Fatalf("%v", err)
	}
}

func TestChain(t *testing.T) {
	names := func(ls []Layer) (out []string) {
		for _, l := range ls {
			out = append(out, l.Name+"="+l.Dir)
		}
		return out
	}
	if got := strings.Join(names(Chain(config.Config{Profile: "/u"})), " "); got != "bundled= user=/u" {
		t.Fatalf("%s", got)
	}
	if got := strings.Join(names(Chain(config.Config{})), " "); got != "bundled=" {
		t.Fatalf("%s", got)
	}
}

func TestFrontMatterSurvivesCRLFAndABOM(t *testing.T) {
	for name, body := range map[string]string{
		"crlf": "+++\r\noverride = true\r\n+++\r\nP",
		"bom":  "\xef\xbb\xbf+++\noverride = true\n+++\nP",
	} {
		g, err := guidesOf(config.Config{Profile: dir(t, map[string]string{"intent=review.md": body})}).Guide(Review, Facts{})
		if err != nil || texts(g) != "P" {
			t.Errorf("%s: %q %v", name, texts(g), err)
		}
	}
}

func TestAMisplacedFrontMatterIsAnError(t *testing.T) {
	d := dir(t, map[string]string{"intent=review.md": "\n+++\noverride = true\n+++\nX"})
	var f FrontMatterError
	if _, err := Load([]Layer{bundled(), user(d)}, testIntents, nil); !errors.As(err, &f) {
		t.Fatalf("%v", err)
	}
}

func TestSourcesKeepThePathsForStorage(t *testing.T) {
	g := Guide{Parts: []Part{{Layer: bundled(), File: "embedded intent=review.md"}, {Layer: Layer{Name: "user", Dir: "/p"}, File: "/p/intent=review.md"}}}
	if got := g.Sources(); len(got) != 2 || got[0] != "bundled: embedded intent=review.md" || got[1] != "user: /p/intent=review.md" {
		t.Fatal(got)
	}
}

func TestGuidesComposeTheConfiguredChain(t *testing.T) {
	cfg := config.Defaults()
	cfg.Profile = dir(t, map[string]string{"intent=review.md": "USER", "organization=acme/intent=review.md": "ACME"})
	g, err := guidesOf(cfg).Guide(Review, Facts{Owner: "acme", Repo: "api"})
	if err != nil || len(g.Parts) != 3 || g.Parts[1].Text != "USER" || g.Parts[2].Text != "ACME" {
		t.Fatalf("%+v %v", g.Parts, err)
	}
	if err := guidesOf(cfg).Check(); err != nil {
		t.Fatal(err)
	}
	cfg.Profile = dir(t, map[string]string{"organization=acme/review.md": "BAD"})
	if err := guidesOf(cfg).Check(); err == nil {
		t.Fatal("a misnamed block passed the check")
	}
}

func guidesOf(cfg config.Config) Guides {
	return Guides{Tree: func() (Tree, error) { return Load(Chain(cfg), testIntents, nil) }}
}
