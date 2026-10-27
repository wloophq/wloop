package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/setup"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// testHomeKinds run only a claude found under the temporary directory, so a
// test can never reach the developer's real one.
func testHomeKinds(t *testing.T) map[string]setup.HomeKind {
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	sh := func(ctx context.Context, dir string, env []string, stdin, bin string, args ...string) ([]byte, error) {
		path, err := exec.LookPath(bin)
		if err != nil {
			return nil, err
		}
		if real, _ := filepath.EvalSymlinks(path); tmp == "" || !strings.HasPrefix(real, tmp+string(filepath.Separator)) {
			t.Errorf("refusing to run %s: it is not a fake under %s", path, tmp)
			return nil, os.ErrPermission
		}
		return claude.Shell(ctx, dir, env, stdin, path, args...)
	}
	return map[string]setup.HomeKind{"claude": claude.NewHomeKind(sh)}
}

const fakeClaudeScript = `#!/bin/sh
exec python3 -I - "$@" <<'PY'
import json, os, sys
a = sys.argv[1:]
if len(a) < 5 or a[0] != "mcp" or a[2:4] != ["--scope", "user"] or a[1] not in ("add-json", "remove"):
    sys.exit("fake claude: unexpected " + " ".join(a))
d = os.environ.get("CLAUDE_CONFIG_DIR") or os.environ["HOME"]
p = os.path.join(d, ".claude.json")
doc = json.load(open(p)) if os.path.exists(p) else {}
s = doc.setdefault("mcpServers", {})
if a[1] == "add-json":
    if a[4] in s:
        sys.exit("MCP server " + a[4] + " already exists")
    s[a[4]] = json.loads(a[5])
else:
    if a[4] not in s:
        sys.exit("No MCP server named " + a[4])
    del s[a[4]]
os.makedirs(d, exist_ok=True)
with open(p, "w") as f:
    json.dump(doc, f)
PY
`

func fakeClaude(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "bin")
	writeFile(t, filepath.Join(dir, "claude"), fakeClaudeScript)
	if err := os.Chmod(filepath.Join(dir, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func canonical(t *testing.T, raw string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(buf.String())
}

func readJSON(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(t, string(raw))
}

// tempWorld points every home the test can reach at root.
func tempWorld(t *testing.T) (root, user string) {
	root = t.TempDir()
	user = filepath.Join(root, "user")
	t.Setenv("HOME", user)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "config-dir"))
	t.Setenv("ORCA_USER_DATA_PATH", filepath.Join(user, "Library", "Application Support", "orca"))
	t.Setenv("PATH", fakeClaude(t, root)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, user
}

func mkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstallWritesTheHomeOnce(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "orca-home")
	t.Setenv("HOME", filepath.Join(root, "user"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("PATH", fakeClaude(t, root)+string(os.PathListSeparator)+os.Getenv("PATH"))
	original := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"curl -s localhost:$ORCA_AGENT_HOOK_PORT"}]}]},"permissions":{"allow":["Bash(ls *)"]}}`
	writeFile(t, filepath.Join(home, "settings.json"), original)
	te := newTestEnv(t, &fake.Manager{})
	te.withCallerHome(home)
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	settings := readJSON(t, filepath.Join(home, "settings.json"))
	for _, want := range []string{"hook stop || true", "hook session-start || true", brand.ToolPrefix() + "turn_end", "ORCA_AGENT_HOOK_PORT", "Bash(ls *)"} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings.json lacks %q", want)
		}
	}
	if mcp := readJSON(t, filepath.Join(home, ".claude.json")); !strings.Contains(mcp, ` hook mcp-headers"`) {
		t.Errorf("registration: %s", mcp)
	}
	before := readJSON(t, filepath.Join(home, "settings.json"))
	te.run("install", "--home", home)
	if readJSON(t, filepath.Join(home, "settings.json")) != before {
		t.Fatal("a second install changed settings.json")
	}
}

func TestInstallRegistersTheConfiguredMCPPort(t *testing.T) {
	root, _ := tempWorld(t)
	home := filepath.Join(root, "home")
	mkdir(t, home)
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.MCP.Port = 47811
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if mcp := readJSON(t, filepath.Join(home, ".claude.json")); !strings.Contains(mcp, `"url":"http://127.0.0.1:47811/mcp"`) {
		t.Fatalf("registration: %s", mcp)
	}
}

func backups(t *testing.T, path string) []string {
	t.Helper()
	got, err := filepath.Glob(path + ".loop-*.bak")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestInstallBacksUpEveryFile(t *testing.T) {
	root, _ := tempWorld(t)
	home := filepath.Join(root, "home")
	settings := `{"model":"opus"}`
	registrations := `{"mcpServers":{"other":{"type":"stdio","command":"x"}}}`
	writeFile(t, filepath.Join(home, "settings.json"), settings)
	writeFile(t, filepath.Join(home, ".claude.json"), registrations)
	te := newTestEnv(t, &fake.Manager{})
	writeFile(t, filepath.Join(home, "skills", brand.Name, "SKILL.md"), "mine")
	writeFile(t, filepath.Join(home, "skills", brand.Name+"-review", "SKILL.md"), string(te.renderSkill(bundledSkill(t, brand.Name+"-review"))))
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for path, before := range map[string]string{
		filepath.Join(home, "settings.json"): settings,
		filepath.Join(home, ".claude.json"):  registrations,
	} {
		got := backups(t, path)
		if len(got) != 1 {
			t.Errorf("%s: backups %q", path, got)
			continue
		}
		if raw, _ := os.ReadFile(got[0]); string(raw) != before {
			t.Errorf("%s holds %q, want %q", got[0], raw, before)
		}
		if !strings.Contains(te.out.String(), got[0]) {
			t.Errorf("the output does not name %s:\n%s", got[0], te.out)
		}
	}
	for _, skill := range []string{brand.Name + "-review", brand.Name} {
		if got := backups(t, filepath.Join(home, "skills", skill, "SKILL.md")); len(got) != 0 {
			t.Errorf("skill %s was backed up: %q", skill, got)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(home, "skills", brand.Name, "SKILL.md")); string(raw) != "mine" {
		t.Errorf("a skill install did not write was replaced: %q", raw)
	}
	te.run("install", "--home", home)
	if got := backups(t, filepath.Join(home, "settings.json")); len(got) != 1 {
		t.Errorf("a second install backed up an unchanged file: %q", got)
	}
}

func TestDefaultHomeRunsWithoutConfigDir(t *testing.T) {
	root, user := tempWorld(t)
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	mkdir(t, filepath.Join(user, ".claude"))
	if code := te.run("install", "--home", filepath.Join(user, ".claude")); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if mcp := readJSON(t, filepath.Join(user, ".claude.json")); !strings.Contains(mcp, `"`+brand.Name+`":`) {
		t.Fatalf("registration: %s", mcp)
	}
	for _, leaked := range []string{filepath.Join(root, "config-dir", ".claude.json"), filepath.Join(user, ".claude", ".claude.json")} {
		if _, err := os.Stat(leaked); err == nil {
			t.Errorf("claude wrote %s", leaked)
		}
	}
}

func TestInstallFindsEveryHome(t *testing.T) {
	root, user := tempWorld(t)
	orca := filepath.Join(user, "Library", "Application Support", "orca", "claude-profiles", "p1", "home")
	if err := os.MkdirAll(orca, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgHome := filepath.Join(root, "work-home")
	mkdir(t, cfgHome, filepath.Join(user, ".claude"), filepath.Join(root, "config-dir"))
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	te.withCallerHome(filepath.Join(root, "config-dir"))
	te.cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: cfgHome}}
	if code := te.run("install"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	homes := []string{filepath.Join(user, ".claude"), filepath.Join(root, "config-dir"), cfgHome, orca}
	if got := te.installHomes(te.cfg); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(homes))) {
		t.Errorf("homes %q, want %q", got, homes)
	}
	for _, home := range homes {
		if raw, err := os.ReadFile(filepath.Join(home, "settings.json")); err != nil || !strings.Contains(string(raw), "hook stop || true") {
			t.Errorf("%s: %v", home, err)
		}
	}
	for _, dir := range append(homes, filepath.Join(user, ".agents")) {
		if _, err := os.Stat(filepath.Join(dir, "skills", brand.Name+"-review", "SKILL.md")); err != nil {
			t.Errorf("skill: %v", err)
		}
	}
	if mcp := readJSON(t, filepath.Join(user, ".claude.json")); !strings.Contains(mcp, `"`+brand.Name+`":`) {
		t.Errorf("default home registration: %s", mcp)
	}
}

func TestInstallDryRunChangesNothing(t *testing.T) {
	root, _ := tempWorld(t)
	home := filepath.Join(root, "home")
	writeFile(t, filepath.Join(home, "settings.json"), `{}`)
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("install", "--dry-run", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if entries, _ := listDir(home); !slices.Equal(entries, []string{"settings.json"}) {
		t.Fatalf("dry run wrote %v", entries)
	}
	if !strings.Contains(te.out.String(), "would add hook Stop") || !strings.Contains(te.out.String(), "would add skill "+brand.Name+"-review") {
		t.Fatalf("%s", te.out)
	}
}

func TestInstallWritesThroughASymlinkedSkillsRootOnce(t *testing.T) {
	root, user := tempWorld(t)
	dotfiles := filepath.Join(root, "dotfiles", "skills")
	claudeHome := filepath.Join(user, ".claude")
	orca := filepath.Join(user, "Library", "Application Support", "orca", "claude-profiles", "p1", "home")
	mkdir(t, dotfiles, claudeHome, orca)
	if err := os.Symlink(dotfiles, filepath.Join(claudeHome, "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(claudeHome, "skills"), filepath.Join(orca, "skills")); err != nil {
		t.Fatal(err)
	}
	te := newTestEnv(t, &fake.Manager{})
	writeFile(t, filepath.Join(dotfiles, brand.Name, "SKILL.md"), string(bundledSkill(t, brand.Name).Render(te.command(), "0.0.1")))
	te.Home = user
	if code := te.run("install", "--home", claudeHome, "--home", orca); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dotfiles, brand.Name, "SKILL.md")); string(raw) != string(te.renderSkill(bundledSkill(t, brand.Name))) {
		t.Fatalf("skill not written through the link: %q", raw)
	}
	if got := backups(t, filepath.Join(dotfiles, brand.Name, "SKILL.md")); len(got) != 0 {
		t.Fatalf("backups %q", got)
	}
	if n := strings.Count(te.out.String(), "skill "+brand.Name+"-review in "); n != 2 {
		t.Fatalf("the shared skills directory and ~/.agents took %d writes:\n%s", n, te.out)
	}
	for _, link := range []string{filepath.Join(claudeHome, "skills"), filepath.Join(orca, "skills")} {
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is no longer a link: %v", link, err)
		}
	}
	te.out.Reset()
	if code := te.run("install", "--home", claudeHome, "--home", orca); code != 0 || te.out.Len() != 0 {
		t.Fatalf("a second install changed: exit %d\n%s", code, te.out)
	}
}

func TestInstallSkipsASymlinkedSkillDir(t *testing.T) {
	root, _ := tempWorld(t)
	home := filepath.Join(root, "home")
	elsewhere := filepath.Join(root, "mine")
	writeFile(t, filepath.Join(elsewhere, "SKILL.md"), "mine")
	mkdir(t, filepath.Join(home, "skills"))
	if err := os.Symlink(elsewhere, filepath.Join(home, "skills", brand.Name)); err != nil {
		t.Fatal(err)
	}
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if raw, _ := os.ReadFile(filepath.Join(elsewhere, "SKILL.md")); string(raw) != "mine" {
		t.Fatalf("wrote through a linked skill: %q", raw)
	}
	if !strings.Contains(te.out.String(), "skipped skill "+brand.Name) {
		t.Fatalf("%s", te.out)
	}
	if c := te.skillCheck(home, "fix"); c.mark != markMissing || !strings.Contains(c.fix, filepath.Join(home, "skills", brand.Name)) {
		t.Fatalf("%+v", c)
	}
}

func TestInstallSkipsAnAbsentHome(t *testing.T) {
	root, _ := tempWorld(t)
	absent := filepath.Join(root, "nowhere")
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("install", "--home", absent); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if _, err := os.Stat(absent); err == nil {
		t.Fatal("install created an absent home")
	}
	if !strings.Contains(te.out.String(), "skipped "+absent+": does not exist") {
		t.Fatalf("%s", te.out)
	}
}

func TestInstallPrintsABundledSkill(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("install", "--print-skill", brand.Name+"-review"); code != 0 || te.out.String() != string(te.renderSkill(bundledSkill(t, brand.Name+"-review"))) {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("install", "--print-skill", "nope"); code == 0 {
		t.Fatal("printed a skill that is not bundled")
	}
}

func TestInstallSkillsRenderTheCommandName(t *testing.T) {
	root, _ := tempWorld(t)
	home := filepath.Join(root, "home")
	mkdir(t, home)
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "skills", brand.Name+"-review", "SKILL.md"))
	if cmd := te.command(); cmd != brand.Command || string(raw) != string(te.renderSkill(bundledSkill(t, brand.Name+"-review"))) || !strings.Contains(string(raw), "`"+cmd+" instructions review <url> -- '<guidance>'`") {
		t.Fatalf("%s", raw)
	}
	if _, err := os.Stat(filepath.Join(te.Home, ".agents", "skills", brand.Name, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallDryRunOfProjectsWithoutAgentIsPinned(t *testing.T) {
	root, user := tempWorld(t)
	orca := filepath.Join(user, "Library", "Application Support", "orca", "claude-profiles", "p1", "home")
	work, oss := filepath.Join(root, "work-home"), filepath.Join(root, "oss-home")
	mkdir(t, orca, work, oss, filepath.Join(user, ".claude"))
	writeFile(t, filepath.Join(work, "settings.json"), `{"model":"x"}`)
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	te.cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: work}, {Match: "oss/*", AgentHome: oss}}
	if code := te.run("install", "--dry-run"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	real, _ := filepath.EvalSymlinks(root)
	got := strings.ReplaceAll(strings.ReplaceAll(te.out.String(), real, "<root>"), root, "<root>")
	if got != installDryRunPin {
		t.Fatalf("dry run output:\n%s", got)
	}
}

var installDryRunPin = `would add MCP server ` + brand.Name + ` in <root>/user/.claude.json
would add hook Stop in <root>/user/.claude/settings.json
would add hook SessionStart in <root>/user/.claude/settings.json
would add hook UserPromptSubmit in <root>/user/.claude/settings.json
would add permission ` + brand.ToolPrefix() + `turn_end in <root>/user/.claude/settings.json
would add permission ` + brand.ToolPrefix() + `slack_send_message in <root>/user/.claude/settings.json
would add permission ` + brand.ToolPrefix() + `slack_react in <root>/user/.claude/settings.json
would add permission ` + brand.ToolPrefix() + `slack_edit in <root>/user/.claude/settings.json
would add MCP server ` + brand.Name + ` in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/.claude.json
would add hook Stop in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/settings.json
would add hook SessionStart in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/settings.json
would add hook UserPromptSubmit in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/settings.json
would add permission ` + brand.ToolPrefix() + `turn_end in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_send_message in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_react in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_edit in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/settings.json
would back up <root>/work-home/settings.json
would add MCP server ` + brand.Name + ` in <root>/work-home/.claude.json
would add hook Stop in <root>/work-home/settings.json
would add hook SessionStart in <root>/work-home/settings.json
would add hook UserPromptSubmit in <root>/work-home/settings.json
would add permission ` + brand.ToolPrefix() + `turn_end in <root>/work-home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_send_message in <root>/work-home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_react in <root>/work-home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_edit in <root>/work-home/settings.json
would add MCP server ` + brand.Name + ` in <root>/oss-home/.claude.json
would add hook Stop in <root>/oss-home/settings.json
would add hook SessionStart in <root>/oss-home/settings.json
would add hook UserPromptSubmit in <root>/oss-home/settings.json
would add permission ` + brand.ToolPrefix() + `turn_end in <root>/oss-home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_send_message in <root>/oss-home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_react in <root>/oss-home/settings.json
would add permission ` + brand.ToolPrefix() + `slack_edit in <root>/oss-home/settings.json
would add skill ` + brand.Name + `-address in <root>/user/.claude/skills/` + brand.Name + `-address/SKILL.md
would add skill ` + brand.Name + `-nudge in <root>/user/.claude/skills/` + brand.Name + `-nudge/SKILL.md
would add skill ` + brand.Name + `-review in <root>/user/.claude/skills/` + brand.Name + `-review/SKILL.md
would add skill ` + brand.Name + ` in <root>/user/.claude/skills/` + brand.Name + `/SKILL.md
would add skill ` + brand.Name + `-profile in <root>/user/.claude/skills/` + brand.Name + `-profile/SKILL.md
would add skill ` + brand.Name + `-address in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/skills/` + brand.Name + `-address/SKILL.md
would add skill ` + brand.Name + `-nudge in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/skills/` + brand.Name + `-nudge/SKILL.md
would add skill ` + brand.Name + `-review in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/skills/` + brand.Name + `-review/SKILL.md
would add skill ` + brand.Name + ` in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/skills/` + brand.Name + `/SKILL.md
would add skill ` + brand.Name + `-profile in <root>/user/Library/Application Support/orca/claude-profiles/p1/home/skills/` + brand.Name + `-profile/SKILL.md
would add skill ` + brand.Name + `-address in <root>/work-home/skills/` + brand.Name + `-address/SKILL.md
would add skill ` + brand.Name + `-nudge in <root>/work-home/skills/` + brand.Name + `-nudge/SKILL.md
would add skill ` + brand.Name + `-review in <root>/work-home/skills/` + brand.Name + `-review/SKILL.md
would add skill ` + brand.Name + ` in <root>/work-home/skills/` + brand.Name + `/SKILL.md
would add skill ` + brand.Name + `-profile in <root>/work-home/skills/` + brand.Name + `-profile/SKILL.md
would add skill ` + brand.Name + `-address in <root>/oss-home/skills/` + brand.Name + `-address/SKILL.md
would add skill ` + brand.Name + `-nudge in <root>/oss-home/skills/` + brand.Name + `-nudge/SKILL.md
would add skill ` + brand.Name + `-review in <root>/oss-home/skills/` + brand.Name + `-review/SKILL.md
would add skill ` + brand.Name + ` in <root>/oss-home/skills/` + brand.Name + `/SKILL.md
would add skill ` + brand.Name + `-profile in <root>/oss-home/skills/` + brand.Name + `-profile/SKILL.md
would add skill ` + brand.Name + `-address in <root>/user/.agents/skills/` + brand.Name + `-address/SKILL.md
would add skill ` + brand.Name + `-nudge in <root>/user/.agents/skills/` + brand.Name + `-nudge/SKILL.md
would add skill ` + brand.Name + `-review in <root>/user/.agents/skills/` + brand.Name + `-review/SKILL.md
would add skill ` + brand.Name + ` in <root>/user/.agents/skills/` + brand.Name + `/SKILL.md
would add skill ` + brand.Name + `-profile in <root>/user/.agents/skills/` + brand.Name + `-profile/SKILL.md
`

func TestHomesAreGroupedByTheProjectsKind(t *testing.T) {
	root, user := tempWorld(t)
	work, echoHome := filepath.Join(root, "work-home"), filepath.Join(root, "echo-home")
	mkdir(t, work, echoHome, filepath.Join(user, ".claude"))
	te := newTestEnv(t, &fake.Manager{})
	te.withEcho()
	te.Home = user
	te.cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: work}, {Match: "oss/*", Agent: "echo", AgentHome: echoHome}}
	got := te.homesByKind(te.cfg)
	if want := []string{filepath.Join(user, ".claude"), work}; !slices.Equal(got["claude"], want) {
		t.Errorf("claude homes %q, want %q", got["claude"], want)
	}
	if want := []string{echoHome}; !slices.Equal(got["echo"], want) {
		t.Errorf("echo homes %q, want %q", got["echo"], want)
	}
	if k := te.kindOfHome(te.cfg, echoHome+"/"); k != "echo" {
		t.Errorf("kind of the echo home %q", k)
	}
	if k := te.kindOfHome(te.cfg, filepath.Join(root, "elsewhere")); k != "claude" {
		t.Errorf("kind of an unknown home %q", k)
	}
	if code := te.run("install", "--dry-run"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	if !strings.Contains(out, "echo installs into "+echoHome+"\n") || strings.Contains(out, "echo installs into "+work) {
		t.Errorf("echo's lines:\n%s", out)
	}
	if strings.Contains(out, "would add hook Stop in "+echoHome) || !strings.Contains(out, "would add hook Stop in "+work) {
		t.Errorf("claude's lines:\n%s", out)
	}
}

func TestInstallRefusesAHomeAnotherKindOwns(t *testing.T) {
	root, _ := tempWorld(t)
	claudeHome, plain := filepath.Join(root, "claude-home"), filepath.Join(root, "plain")
	writeFile(t, filepath.Join(claudeHome, "settings.json"), `{}`)
	mkdir(t, plain)
	te := newTestEnv(t, &fake.Manager{})
	te.withEcho()
	te.cfg.Agent = "echo"
	if code := te.run("install", "--home", plain, "--home", claudeHome); code == 0 {
		t.Fatalf("installed echo into a claude home: %s", te.out)
	}
	if want := "refusing " + claudeHome + ": it is a home of claude, not of echo"; !strings.Contains(te.err.String(), want) {
		t.Fatalf("stderr %q, want %q", te.err, want)
	}
	if te.out.Len() != 0 {
		t.Fatalf("install wrote before refusing:\n%s", te.out)
	}
	if raw, _ := os.ReadFile(filepath.Join(claudeHome, "settings.json")); string(raw) != `{}` {
		t.Fatalf("settings.json %s", raw)
	}
	te.out.Reset()
	if code := te.run("install", "--dry-run", "--home", plain); code != 0 || !strings.Contains(te.out.String(), "echo installs into "+plain) {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

func TestFoundHomesWithTwoOrcaProfilesArePinned(t *testing.T) {
	_, user := tempWorld(t)
	profiles := filepath.Join(user, "Library", "Application Support", "orca", "claude-profiles")
	mkdir(t, filepath.Join(profiles, "b2", "home"), filepath.Join(profiles, "a1", "home"), filepath.Join(profiles, "c3"))
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	te.withCallerHome("/caller")
	want := []string{filepath.Join(user, ".claude"), "/caller", filepath.Join(profiles, "a1", "home"), filepath.Join(profiles, "b2", "home")}
	if got := te.Homes["claude"].Find(te.homeSpec(te.cfg)); !slices.Equal(got, want) {
		t.Fatalf("found %q, want %q", got, want)
	}
}

func TestInstallKeysTheMCPRegistrationAndDoctorSeesAStaleKey(t *testing.T) {
	root, _ := tempWorld(t)
	home := filepath.Join(root, "home")
	mkdir(t, home)
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	key := te.mcpKey()
	fi, err := os.Stat(te.mcpKeyPath())
	if err != nil || key == "" || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v %q", fi, err, key)
	}
	if mcp := readJSON(t, filepath.Join(home, ".claude.json")); !strings.Contains(mcp, `"X-Loop-Key":"`+key+`"`) {
		t.Fatalf("registration: %s", mcp)
	}
	te.run("install", "--home", home)
	if te.mcpKey() != key {
		t.Fatal("a second install changed the key")
	}
	mcpCheck := func() check {
		t.Helper()
		kind, _ := te.homeKind(te.cfg.Agent)
		for _, c := range te.kindHomeChecks(context.Background(), te.cfg, te.cfg.Agent, kind, []string{home}) {
			if strings.HasSuffix(c.name, "MCP server "+brand.MCPServer()) {
				return c
			}
		}
		t.Fatal("no MCP server check")
		return check{}
	}
	if c := mcpCheck(); c.mark != markOK {
		t.Fatalf("after install: %+v", c)
	}
	writeFile(t, te.mcpKeyPath(), "other\n")
	if c := mcpCheck(); c.mark != markMissing || !strings.Contains(c.info, "X-Loop-Key is stale") || strings.Contains(c.info, key) {
		t.Fatalf("stale: %+v", c)
	}
}
