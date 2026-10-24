package claude

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/shell"
)

const dossierPath = "/state/dossiers/github/owner/repo%2312#"

func dossierPolicy(path string) turns.Policy {
	return turns.Policy{Deny: [][]string{{"git", "push"}}, TurnEnd: true,
		Dirs: []turns.Dir{{Path: path, Write: []string{"log", "sources", "drafts"}}}}
}

// shellWords is how sh splits line: its arguments after the command name.
func shellWords(t *testing.T, line string) []string {
	t.Helper()
	out, err := exec.Command("/bin/sh", "-c", `set -- `+strings.TrimPrefix(line, "claude ")+`; printf '%s\0' "$@"`).Output()
	if err != nil {
		t.Fatalf("sh could not split %q: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

func settingsOf(t *testing.T, words []string) []string {
	t.Helper()
	i := slices.Index(words, "--settings")
	if i < 0 {
		t.Fatalf("no --settings in %q", words)
	}
	var s struct {
		Permissions struct{ Allow []string } `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(words[i+1]), &s); err != nil {
		t.Fatal(err)
	}
	return s.Permissions.Allow
}

func TestPolicyGrantsTheDossier(t *testing.T) {
	line, err := Agent{}.Command(turns.AgentSession{Policy: dossierPolicy(dossierPath)})
	if err != nil {
		t.Fatal(err)
	}
	words := shellWords(t, line)
	if i := slices.Index(words, "--add-dir"); i < 0 || words[i+1] != dossierPath {
		t.Fatalf("no --add-dir %s in %q", dossierPath, words)
	}
	allow := settingsOf(t, words)
	for _, sub := range []string{"log", "sources", "drafts"} {
		if rule := "Edit(/" + dossierPath + "/" + sub + "/**)"; !slices.Contains(allow, rule) {
			t.Errorf("no %s in %q", rule, allow)
		}
	}
	if strings.Contains(line, "notes.md") {
		t.Errorf("a rule names notes.md: %s", line)
	}
	i := slices.Index(words, "--settings")
	var s struct {
		Permissions struct{ Deny []string } `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(words[i+1]), &s); err != nil {
		t.Fatal(err)
	}
	if rule := "Edit(/" + dossierPath + "/.git/**)"; !slices.Contains(s.Permissions.Deny, rule) {
		t.Errorf("no deny %s in %q", rule, s.Permissions.Deny)
	}
}

func TestAddDirIsQuoted(t *testing.T) {
	path := `/Users/me/it's a "state"/dossiers/slack/T000/C000/1700.1#`
	line, err := Agent{}.Command(turns.AgentSession{Policy: dossierPolicy(path)})
	if err != nil {
		t.Fatal(err)
	}
	words := shellWords(t, line)
	if i := slices.Index(words, "--add-dir"); i < 0 || words[i+1] != path {
		t.Fatalf("--add-dir did not parse back to %q: %q", path, words)
	}
	if allow := settingsOf(t, words); !slices.Contains(allow, "Edit(/"+path+"/log/**)") {
		t.Fatalf("the log rule did not parse back: %q", allow)
	}
}

func TestDirsOnlyPolicyPrintsFlags(t *testing.T) {
	p := turns.Policy{Dirs: []turns.Dir{{Path: dossierPath}}}
	if emptyPolicy(p) {
		t.Fatal("a policy with only Dirs is empty")
	}
	line, _ := Agent{}.Command(turns.AgentSession{Policy: p})
	if !strings.Contains(line, "--add-dir "+shell.Quote(dossierPath)) || !strings.Contains(line, "--settings") {
		t.Fatalf("flags missing: %s", line)
	}
}

// commandBefore is Command as it was before policies carried directories.
func commandBefore(k Agent, s turns.AgentSession) string {
	parts := []string{"claude"}
	switch {
	case s.Home == "":
	case k.usesDefault(s.Home):
		parts = append([]string{"env", "-u", "CLAUDE_CONFIG_DIR"}, parts...)
	default:
		parts = append([]string{"CLAUDE_CONFIG_DIR=" + shell.Quote(s.Home)}, parts...)
	}
	if s.Model != "" {
		parts = append(parts, "--model", shell.Quote(s.Model))
	}
	p := s.Policy
	if len(p.Allow) == 0 && len(p.Deny) == 0 && len(p.WritePaths) == 0 && !p.TurnEnd && !p.Drafts && !p.NoGitHubWrites && !p.NoMCP && !p.Strict {
		return strings.Join(parts, " ")
	}
	parts = append(parts, "--settings", shell.Quote(k.Settings(p)))
	if p.Strict {
		parts = append(parts, "--permission-mode", "default", "--setting-sources", "user")
	}
	if p.NoMCP {
		parts = append(parts, "--strict-mcp-config")
	}
	return strings.Join(parts, " ")
}

func TestPolicyWithoutDossierUnchanged(t *testing.T) {
	k := Agent{UserHome: "/home/me"}
	for _, s := range []turns.AgentSession{
		{},
		{Home: "/home/me/.claude"},
		{Home: "/h/p's", Model: "opus"},
		{Policy: turns.Policy{TurnEnd: true}},
		{Policy: turns.Policy{Drafts: true}},
		{Policy: turns.Policy{Allow: [][]string{{"gh", "pr", "view"}}, Deny: [][]string{{"git", "push"}}, WritePaths: []string{".scratch"}, TurnEnd: true, NoGitHubWrites: true}},
		{Policy: turns.Policy{Strict: true, NoMCP: true, NoGitHubWrites: true}, Model: "haiku"},
	} {
		got, err := k.Command(s)
		if want := commandBefore(k, s); err != nil || got != want {
			t.Errorf("Command(%+v) = %q, %v; before it was %q", s, got, err, want)
		}
	}
}
