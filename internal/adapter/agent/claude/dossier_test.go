package claude

import (
	"context"
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

// grantedDirs is the directories the --settings of words grants; a session
// stops on a question about each directory a flag grants instead.
func grantedDirs(t *testing.T, words []string) []string {
	t.Helper()
	if slices.Contains(words, "--add-dir") {
		t.Fatalf("a flag grants a directory: %q", words)
	}
	i := slices.Index(words, "--settings")
	if i < 0 {
		t.Fatalf("no --settings in %q", words)
	}
	var s struct {
		Permissions struct {
			AdditionalDirectories []string `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(words[i+1]), &s); err != nil {
		t.Fatal(err)
	}
	return s.Permissions.AdditionalDirectories
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
	if dirs := grantedDirs(t, words); !slices.Equal(dirs, []string{dossierPath}) {
		t.Fatalf("the settings grant %q, want %s", dirs, dossierPath)
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

func TestHeadlessGrantsTheDossierThroughSettings(t *testing.T) {
	var argv []string
	sh := func(_ context.Context, _ string, _ []string, _, _ string, args ...string) ([]byte, error) {
		argv = args
		return nil, nil
	}
	s := turns.AgentSession{Worktree: t.TempDir(), Policy: dossierPolicy(dossierPath)}
	if _, err := (Agent{Sh: sh}).Headless(context.Background(), s, "do it"); err != nil {
		t.Fatal(err)
	}
	if dirs := grantedDirs(t, argv); !slices.Equal(dirs, []string{dossierPath}) {
		t.Fatalf("the settings grant %q, want %s", dirs, dossierPath)
	}
}

func TestAGrantedDirIsQuoted(t *testing.T) {
	path := `/Users/me/it's a "state"/dossiers/slack/T000/C000/1700.1#`
	line, err := Agent{}.Command(turns.AgentSession{Policy: dossierPolicy(path)})
	if err != nil {
		t.Fatal(err)
	}
	words := shellWords(t, line)
	if dirs := grantedDirs(t, words); !slices.Equal(dirs, []string{path}) {
		t.Fatalf("the granted directory did not parse back to %q: %q", path, dirs)
	}
	if allow := settingsOf(t, words); !slices.Contains(allow, "Edit(/"+path+"/log/**)") {
		t.Fatalf("the log rule did not parse back: %q", allow)
	}
}

func TestDirsOnlyPolicyGrantsThroughSettings(t *testing.T) {
	p := turns.Policy{Dirs: []turns.Dir{{Path: dossierPath}}}
	if emptyPolicy(p) {
		t.Fatal("a policy with only Dirs is empty")
	}
	line, _ := Agent{}.Command(turns.AgentSession{Policy: p})
	if dirs := grantedDirs(t, shellWords(t, line)); !slices.Equal(dirs, []string{dossierPath}) {
		t.Fatalf("the settings grant %q, want %s", dirs, dossierPath)
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
	if len(p.Allow) == 0 && len(p.Deny) == 0 && len(p.WritePaths) == 0 && !p.TurnEnd && len(p.DraftTools) == 0 && !p.NoMCP && !p.Strict {
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
		{Policy: turns.Policy{DraftTools: []string{"draft_read"}}},
		{Policy: turns.Policy{Allow: [][]string{{"gh", "pr", "view"}}, Deny: [][]string{{"git", "push"}}, WritePaths: []string{".scratch"}, TurnEnd: true}},
		{Policy: turns.Policy{Strict: true, NoMCP: true}, Model: "haiku"},
	} {
		got, err := k.Command(s)
		if want := commandBefore(k, s); err != nil || got != want {
			t.Errorf("Command(%+v) = %q, %v; before it was %q", s, got, err, want)
		}
	}
}

func TestADirWithNoWriteDeniesEveryEdit(t *testing.T) {
	const merged = "/state/dossiers/task/t-merged#"
	p := dossierPolicy(dossierPath)
	p.Dirs = append(p.Dirs, turns.Dir{Path: merged})
	var s struct {
		Permissions struct {
			Allow, Deny           []string
			AdditionalDirectories []string `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(Agent{}.Settings(p)), &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.Permissions.Deny, "Edit(/"+merged+"/**)") || !slices.Contains(s.Permissions.AdditionalDirectories, merged) {
		t.Fatalf("a read-only dir is not read only: %+v", s.Permissions)
	}
	if slices.Contains(s.Permissions.Deny, "Edit(/"+dossierPath+"/**)") {
		t.Fatalf("a writable dir is denied every edit: %+v", s.Permissions.Deny)
	}
}
