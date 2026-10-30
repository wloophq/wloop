package dossier

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestDirNestsKeySegments(t *testing.T) {
	state := filepath.Join(string(filepath.Separator), "state")
	cases := []struct {
		owner Owner
		want  string
	}{
		{Owner{Unit: unit.Unit{Provider: "github", Key: "owner/repo#12"}}, "dossiers/github/owner/repo%2312#"},
		{Owner{Unit: unit.Unit{Provider: "slack", Key: "T000/C000/1700.1"}}, "dossiers/slack/T000/C000/1700.1#"},
		{Owner{Unit: unit.Unit{Provider: "slack", Key: "T000/C 0?/x"}}, "dossiers/slack/T000/C%200%3F/x#"},
		{Owner{Unit: unit.Unit{Provider: "task", Key: "t-00000000000000a1"}}, "dossiers/task/t-00000000000000a1#"},
		{Owner{Unit: unit.Unit{Provider: "project", Key: "launch-v2"}}, "dossiers/project/launch-v2#"},
		{Owner{Unit: unit.Unit{Provider: "task", Key: "t-b"}, Merged: "t-a"}, "dossiers/task/t-b#/merged/t-a"},
		{Owner{Unit: unit.Unit{Provider: "task", Key: "t-b"}, Merged: "github/acme/api#7"}, "dossiers/task/t-b#/merged/github%2Facme%2Fapi%237"},
		{Owner{Unit: unit.Unit{Provider: "task", Key: "t-b"}, Merged: ".x"}, "dossiers/task/t-b#/merged/%2Ex"},
		{Owner{Unit: unit.Unit{Provider: "github", Key: "owner/.github#3"}}, "dossiers/github/owner/%2Egithub%233#"},
		{Owner{Unit: unit.Unit{Provider: "fake", Key: "a/.git/x#"}}, "dossiers/fake/a/%2Egit/x%23#"},
	}
	for _, c := range cases {
		got, err := Dir(state, c.owner)
		if err != nil {
			t.Fatalf("%v: %v", c.owner, err)
		}
		if want := filepath.Join(state, filepath.FromSlash(c.want)); got != want {
			t.Errorf("Dir(%v) = %s, want %s", c.owner, got, want)
		}
		rel, err := Rel(c.owner)
		if err != nil {
			t.Fatal(err)
		}
		back, err := OwnerOf(rel)
		if err != nil || back != c.owner {
			t.Errorf("OwnerOf(%s) = %v, %v; want %v", rel, back, err, c.owner)
		}
		parsed, err := ParseOwner(c.owner.String())
		if err != nil || parsed != c.owner {
			t.Errorf("ParseOwner(%s) = %v, %v", c.owner, parsed, err)
		}
	}
}

func TestDirRefusesEscapes(t *testing.T) {
	state := filepath.Join(string(filepath.Separator), "state")
	bad := []Owner{
		{},
		{Unit: unit.Unit{Provider: "github", Key: "../x"}},
		{Unit: unit.Unit{Provider: "github", Key: "a/../../x"}},
		{Unit: unit.Unit{Provider: "github", Key: "a/./b"}},
		{Unit: unit.Unit{Provider: "github", Key: "a//b"}},
		{Unit: unit.Unit{Provider: "github", Key: ""}},
		{Unit: unit.Unit{Provider: "github", Key: "/abs"}},
		{Unit: unit.Unit{Provider: "..", Key: "x"}},
		{Unit: unit.Unit{Provider: ".archive", Key: "x"}},
		{Unit: unit.Unit{Provider: "merged", Key: "x"}},
		{Unit: unit.Unit{Provider: "a/b", Key: "x"}},
		{Merged: "t-a"},
		{Unit: unit.Unit{Provider: "task", Key: "t-b"}, Merged: ".."},
	}
	for _, o := range bad {
		if got, err := Dir(state, o); err == nil {
			t.Errorf("Dir(%+v) = %s, want a refusal", o, got)
		}
	}
	if _, err := Dir("relative", Owner{Unit: unit.Unit{Provider: "task", Key: "t-a"}}); err == nil {
		t.Error("a relative state dir was accepted")
	}
	for _, rel := range []string{"github#", "task/t-b#/merged/a/b", "task/t-b/merged/t-a", "task/t-b#/other/t-a", "merged/t-a#", "github/a%2Fb#", "github/../x#", "github/a", "github/a#/b#", "github/.git#"} {
		if o, err := OwnerOf(rel); err == nil {
			t.Errorf("OwnerOf(%s) = %v, want a refusal", rel, o)
		}
	}
	got, _ := Dir(state, Owner{Unit: unit.Unit{Provider: "github", Key: "o/r#1"}})
	if !strings.HasPrefix(got, filepath.Join(state, RootDir, "github")+string(filepath.Separator)) {
		t.Errorf("%s left dossiers/github", got)
	}
}

func TestNoDossierNestsInAnother(t *testing.T) {
	state := filepath.Join(string(filepath.Separator), "state")
	owners := []Owner{
		{Unit: unit.Unit{Provider: "fake", Key: "obj"}},
		{Unit: unit.Unit{Provider: "fake", Key: "obj/seg"}},
		{Unit: unit.Unit{Provider: "fake", Key: "obj#/seg"}},
		{Unit: unit.Unit{Provider: "fake", Key: "obj/.git"}},
		{Unit: unit.Unit{Provider: "fake", Key: "obj/.git/hooks"}},
		{Unit: unit.Unit{Provider: "fake", Key: ".git"}},
	}
	dirs := make([]string, len(owners))
	for i, o := range owners {
		d, err := Dir(state, o)
		if err != nil {
			t.Fatal(err)
		}
		dirs[i] = d
	}
	for i, a := range dirs {
		for j, b := range dirs {
			if i != j && (a == b || strings.HasPrefix(b, a+string(filepath.Separator))) {
				t.Errorf("%s holds %s", a, b)
			}
		}
		for _, seg := range strings.Split(a, string(filepath.Separator)) {
			if seg == ".git" {
				t.Errorf("%s has a .git segment", a)
			}
		}
	}
}
