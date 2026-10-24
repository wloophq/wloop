package skills

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/turn"
)

const version = "1.2.3"

func bundled(t *testing.T) []Skill {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	return Bundled(l.Registry.All())
}

func turnSkills(t *testing.T) []Skill {
	t.Helper()
	all := bundled(t)
	return all[:len(all)-2]
}

func names(sks []Skill) []string {
	var out []string
	for _, sk := range sks {
		out = append(out, sk.Name)
	}
	return out
}

func TestTheTurnSkillsAreTheUserFacingGitHubIntents(t *testing.T) {
	want := []string{brand.SkillName("address"), brand.SkillName("nudge"), brand.SkillName("review"), brand.Name, brand.SkillName("profile")}
	if got := names(bundled(t)); !slices.Equal(got, want) {
		t.Fatalf("skills %v, want %v", got, want)
	}
}

func TestNoSkillKeepsThePlaceholder(t *testing.T) {
	for _, sk := range bundled(t) {
		if body := string(sk.Render(brand.Command, version)); strings.Contains(body, "{{") {
			t.Errorf("%s keeps a placeholder:\n%s", sk.Name, body)
		}
	}
}

func TestThinSkillsCallInstructions(t *testing.T) {
	for _, sk := range turnSkills(t) {
		body := string(sk.Render(brand.Command, version))
		kind := strings.TrimPrefix(sk.Name, brand.Name+"-")
		if !strings.Contains(body, "`"+brand.Command+" instructions "+kind+" <url> -- '<guidance>'`") || !strings.Contains(body, "single quotes") {
			t.Errorf("%s does not pass the guidance after the separator:\n%s", sk.Name, body)
		}
		if !strings.Contains(body, "\nname: "+sk.Name+"\n") || !strings.Contains(body, "`/"+sk.Name+" <url>`") {
			t.Errorf("%s does not name itself:\n%s", sk.Name, body)
		}
		if n := strings.Count(body, "\n"); n >= 26 {
			t.Errorf("%s is %d lines; a thin skill carries no procedure", sk.Name, n)
		}
	}
}

func TestATurnSkillOffersTheIntentsFlags(t *testing.T) {
	for _, sk := range turnSkills(t) {
		body := string(sk.Render(brand.Command, version))
		hasFlags := strings.Contains(body, "`--take-over` takes it over first") && strings.Contains(body, "`--self-review` reviews the change")
		if hasFlags != (sk.Name == brand.SkillName(string(dintent.Address))) {
			t.Errorf("%s flags %v:\n%s", sk.Name, hasFlags, body)
		}
	}
}

func TestTheOperatorSkillKnowsProfilesAndNudge(t *testing.T) {
	body := string(Operator.Render(brand.Command, version))
	for _, want := range []string{"`" + brand.Command + " nudge <url> [guidance]`", "`" + brand.Command + " tui`", "`" + brand.Command + " config get <key>`", "only when the user asks",
		"`" + brand.Command + " profile try <url>`", "serve.err.log", "agent_home"} {
		if !strings.Contains(body, want) {
			t.Errorf(brand.Name+" lacks %s", want)
		}
	}
	for _, gone := range []string{"--profile", "timer", "watch.out.log", "review_guidance", "~/.config/" + brand.StateDir() + "/config.json"} {
		if strings.Contains(body, gone) {
			t.Errorf(brand.Name+" still says %s", gone)
		}
	}
}

func TestSkillsEndTurnsWithTheRecord(t *testing.T) {
	for _, sk := range append(turnSkills(t), Operator) {
		body := string(sk.Render(brand.Command, version))
		for _, want := range []string{"`turn_end`", "`" + brand.Command + " turn end", "`" + brand.Command + " turn answer"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s", sk.Name, want)
			}
		}
	}
}

func TestProfileSkillIsBundled(t *testing.T) {
	all := bundled(t)
	if all[len(all)-1].Name != brand.Name+"-profile" || !strings.HasPrefix(string(Profile.Body), "---\nname: {{name}}-profile\ndescription: ") {
		t.Fatal(names(all))
	}
}

func TestProfileSkillUsesOnlyTheCommand(t *testing.T) {
	raw := string(Profile.Body)
	for _, want := range []string{"{{command}} profile add", "{{command}} profile where", "{{command}} profile check", "without `--apply`", "--apply`", "Never write or rename a block path by hand", ".txt"} {
		if !strings.Contains(raw, want) && !strings.Contains(strings.ToLower(raw), strings.ToLower(want)) {
			t.Errorf("lacks %q", want)
		}
	}
	if strings.Contains(raw, "intent=") {
		t.Error("the skill spells a block path")
	}
}

func TestARenderedSkillCarriesItsMarker(t *testing.T) {
	for _, sk := range bundled(t) {
		raw := sk.Render(brand.Command, version)
		head, _, ok := splitFrontMatter(string(raw))
		if !ok || strings.Count(head, "metadata:") != 1 || !strings.Contains(head, "\nmetadata:\n  managed-by: "+brand.Name+"\n  version: \"1.2.3\"\n  sha256: ") {
			t.Errorf("%s front matter:\n%s", sk.Name, head)
		}
		m, ok := ReadMark(raw)
		if !ok || !m.Unedited || m.Version != version {
			t.Errorf("%s: mark %+v %v", sk.Name, m, ok)
		}
	}
}

func TestAnEditedSkillLosesItsDigestButKeepsItsMarker(t *testing.T) {
	raw := string(Operator.Render(brand.Command, "0.0.9"))
	for name, edited := range map[string]string{
		"body":        strings.Replace(raw, "Operating the review loop", "Running the review loop", 1),
		"description": strings.Replace(raw, "description: Use when", "description: Only when", 1),
	} {
		m, ok := ReadMark([]byte(edited))
		if !ok || m.Unedited || m.Version != "0.0.9" {
			t.Errorf("%s edit: mark %+v %v", name, m, ok)
		}
	}
	for _, foreign := range []string{"mine", "---\nname: x\ndescription: y\n---\nbody\n", "---\nname: x\nmetadata:\n  managed-by: someone-else\n---\n"} {
		if _, ok := ReadMark([]byte(foreign)); ok {
			t.Errorf("%q read as the loop's", foreign)
		}
	}
}

func TestTheTemplateHasOneFrontMatter(t *testing.T) {
	raw, err := os.ReadFile("turn/SKILL.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "---\nname: {{name}}-{{intent}}\ndescription: {{description}}\n---\n") || strings.Contains(string(raw), "metadata:") {
		t.Fatalf("%s", raw)
	}
}
