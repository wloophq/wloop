package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/wloophq/wloop/internal/brand/brandtest"
)

type goreleaserOverride struct {
	Goos   string   `yaml:"goos"`
	Goarch string   `yaml:"goarch"`
	Tags   []string `yaml:"tags"`
}

type goreleaserBuild struct {
	ID        string               `yaml:"id"`
	Env       []string             `yaml:"env"`
	Tags      []string             `yaml:"tags"`
	Overrides []goreleaserOverride `yaml:"overrides"`
}

func TestReleaseBuildsTheBinaryWithTheWindow(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Builds []goreleaserBuild `yaml:"builds"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	// The release names its build after the project, which keeps the default brand.
	id := brandtest.DefaultName
	i := slices.IndexFunc(cfg.Builds, func(b goreleaserBuild) bool { return b.ID == id })
	if i < 0 {
		t.Fatalf("no build with id %s", id)
	}
	b := cfg.Builds[i]
	if !slices.Contains(b.Env, "CGO_ENABLED=0") {
		t.Errorf("the binary's build env is %v, want CGO_ENABLED=0", b.Env)
	}
	if slices.Contains(b.Tags, "purego") {
		t.Error("every target builds with purego; linux would lose the standard library's crypto assembly for nothing")
	}
	for _, arch := range []string{"arm64", "amd64"} {
		if !slices.ContainsFunc(b.Overrides, func(o goreleaserOverride) bool {
			return o.Goos == "darwin" && o.Goarch == arch && slices.Contains(o.Tags, "purego")
		}) {
			t.Errorf("no override builds the binary for darwin/%s with the purego tag: %+v", arch, b.Overrides)
		}
	}
}
