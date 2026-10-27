package decisionlog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func plant(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const zebras = `---
title: Zebras stay striped
description: A zebra keeps its stripes | always.
---

- **Context**: stripes fade.
- **Enforced by**: ` + "`TestZebra`, `TestStripes`" + `, and the stable's checks.
`

const aardvarks = `---
title: aardvarks dig alone
description: An aardvark digs where it is allowed.
---

- **Enforced by**: the burrow's tests.
`

func TestReadSortsByTitleAndNamesTheTests(t *testing.T) {
	t.Parallel()
	dir := plant(t, map[string]string{"zebras-stay-striped.md": zebras, "aardvarks.md": aardvarks, "notes.txt": "not a decision"})
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Decision{
		{Slug: "aardvarks", Title: "aardvarks dig alone", Description: "An aardvark digs where it is allowed."},
		{Slug: "zebras-stay-striped", Title: "Zebras stay striped", Description: "A zebra keeps its stripes | always.", Tests: []string{"TestZebra", "TestStripes"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestReadRefusesAnEntryWithoutItsParts(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"no front matter":  "- **Enforced by**: `TestX`.\n",
		"no title":         "---\ndescription: d.\n---\n\n- **Enforced by**: `TestX`.\n",
		"no description":   "---\ntitle: T\n---\n\n- **Enforced by**: `TestX`.\n",
		"no enforced line": "---\ntitle: T\ndescription: d.\n---\n\n- **Context**: c.\n",
	} {
		if _, err := Read(plant(t, map[string]string{"x.md": src})); err == nil {
			t.Errorf("%s: Read took it", name)
		}
	}
}

func TestReplaceSwapsTheIndexAndKeepsTheProse(t *testing.T) {
	t.Parallel()
	ds := []Decision{
		{Slug: "aardvarks", Title: "aardvarks dig alone", Description: "An aardvark digs."},
		{Slug: "zebras", Title: "Zebras stay striped", Description: "A zebra keeps its stripes | always."},
	}
	page := "---\ntitle: Log\n---\n\nThe criterion.\n\n" + Header + "| [Old](/architecture/decisions/old/) | Gone. |\n\nAfter the table.\n"
	got, err := Replace(page, ds)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\ntitle: Log\n---\n\nThe criterion.\n\n" + Header +
		"| [aardvarks dig alone](/architecture/decisions/aardvarks/) | An aardvark digs. |\n" +
		"| [Zebras stay striped](/architecture/decisions/zebras/) | A zebra keeps its stripes \\| always. |\n" +
		"\nAfter the table.\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if again, _ := Replace(got, ds); again != got {
		t.Error("a second replace changes the page")
	}
	if _, err := Replace(strings.Replace(page, Header, "", 1), ds); err == nil {
		t.Error("a page without the index took it")
	}
}
