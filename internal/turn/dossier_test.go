package turn

import (
	"os"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand/brandtest"
)

const dossierGolden = "testdata/dossier.golden"

func TestInstructionsNameTheDossier(t *testing.T) {
	c := scenario(t, "review/history/injected").Context
	path := "/state/dossiers/github/acme/api%237#"
	c.Dossier = &Dossier{Path: path, Granted: true}
	r, err := render(c)
	if err != nil {
		t.Fatal(err)
	}
	got := section(r.Text, "### Dossier\n")
	c.Dossier.Granted = false
	ungranted, err := render(c)
	if err != nil {
		t.Fatal(err)
	}
	got += "\n---- not granted ----\n" + section(ungranted.Text, "### Dossier\n")
	if *updateGolden {
		brandtest.CanUpdate(t)
		if err := os.WriteFile(dossierGolden, []byte(brandtest.Collapse(got)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(dossierGolden)
	if err != nil {
		t.Fatal(err)
	}
	if want := brandtest.Expand(string(raw)); got != want {
		t.Fatalf("the dossier section differs (go test -run %s -update rewrites it):\n%s", t.Name(), firstDifference(want, got))
	}
	for _, want := range []string{"`" + path + "`", "append to log/, add to sources/\nand drafts/, never rewrite notes.md"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("the instructions lack %q", want)
		}
	}
	if !strings.Contains(ungranted.Text, "reachable on its next launch") || strings.Contains(ungranted.Text, "append to log/") {
		t.Error("an ungranted session is told to write")
	}
}

func TestInstructionsWithoutDossierUnchanged(t *testing.T) {
	for _, s := range Scenarios() {
		if s.Context.Dossier != nil {
			t.Fatalf("%s carries a dossier", s.Name)
		}
		r, err := render(s.Context)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(r.Text, "Dossier") {
			t.Errorf("%s renders a dossier section", s.Name)
		}
	}
	TestScenariosRenderTheirGolden(t)
}
