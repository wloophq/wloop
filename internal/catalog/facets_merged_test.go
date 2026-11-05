package catalog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/turn"
)

type baseProvider = readmodel.Provider

type presenter struct {
	baseProvider
	psource.Catalog
}

func TestAMergedFacetNamesEverySourceInAConflict(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, intentload.ViewsFile), []byte("[facets.waits-on]\nmatch = \"exact\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	l, problems := intentload.Load(append(profile.BundledChain(), profile.Layer{Name: "user", Source: dir, Dir: dir}), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	c := readmodel.BuildCatalog(presenter{Catalog: psource.BundledCatalog(string(unit.GitHub))}, presenter{Catalog: psource.BundledCatalog("slack")})
	got := catalog.CheckFacets(l.Views.Facets, c.Facets)
	if len(got) != 1 || !strings.Contains(got[0].Msg, "github") || !strings.Contains(got[0].Msg, "slack") || !strings.HasSuffix(got[0].File, intentload.ViewsFile) {
		t.Fatalf("problems: %s", got.Error())
	}
}
