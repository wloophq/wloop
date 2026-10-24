package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/skills"
)

func bundledSkill(t *testing.T, name string) skills.Skill {
	t.Helper()
	for _, sk := range bundledSkills() {
		if sk.Name == name {
			return sk
		}
	}
	t.Fatalf("no bundled skill %s", name)
	return skills.Skill{}
}

func TestInstallReplacesASkillAnyVersionWroteAndKeepsAnEditedOne(t *testing.T) {
	root, _ := tempWorld(t)
	home := filepath.Join(root, "home")
	te := newTestEnv(t, &fake.Manager{})
	mkdir(t, filepath.Join(home, "skills"))
	review := filepath.Join(skillsRoot(home), brand.Name+"-review", "SKILL.md")
	operator := filepath.Join(skillsRoot(home), brand.Name, "SKILL.md")
	profile := filepath.Join(skillsRoot(home), brand.Name+"-profile", "SKILL.md")
	older := string(bundledSkill(t, brand.Name+"-review").Render(te.command(), "0.0.1"))
	edited := strings.Replace(string(bundledSkill(t, brand.Name).Render(te.command(), "0.0.1")), "Operating the review loop", "My loop", 1)
	writeFile(t, review, older)
	writeFile(t, operator, edited)
	writeFile(t, profile, "mine")
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if raw, _ := os.ReadFile(review); string(raw) != string(te.renderSkill(bundledSkill(t, brand.Name+"-review"))) {
		t.Errorf("the skill of 0.0.1 was not replaced:\n%s", raw)
	}
	for path, want := range map[string]string{operator: edited, profile: "mine"} {
		if raw, _ := os.ReadFile(path); string(raw) != want {
			t.Errorf("%s replaced:\n%s", path, raw)
		}
	}
	out := te.out.String()
	for _, want := range []string{"changed skill " + brand.Name + "-review in " + review,
		"kept skill " + brand.Name + " at " + operator + ": edited since " + brand.Name + " 0.0.1 installed it",
		"kept skill " + brand.Name + "-profile at " + profile + ": " + brand.Name + " did not write it"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestUninstallRemovesASkillAnyVersionWrote(t *testing.T) {
	w := newUninstallWorld(t)
	review := filepath.Join(skillsRoot(w.claude), brand.Name+"-review", "SKILL.md")
	writeFile(t, review, string(bundledSkill(t, brand.Name+"-review").Render(w.command(), "0.0.1")))
	if code := w.run("uninstall", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, w.err)
	}
	if exists(review) {
		t.Fatalf("the unedited skill of 0.0.1 stayed:\n%s", w.out)
	}
	if strings.Contains(w.out.String(), "differs") || strings.Contains(w.out.String(), "edited") {
		t.Fatalf("an unedited skill was reported as changed:\n%s", w.out)
	}
}
