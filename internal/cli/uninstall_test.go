package cli

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
)

type uninstallWorld struct {
	*testEnv
	sh      *fakeShell
	kc      *fakeKeychain
	srv     *slacktest.Server
	claude  string
	plists  []string
	profile string
}

// newUninstallWorld is a user home with the agent homes' entries, the
// skills, the daemon's and the menu bar's LaunchAgents, the app bundle, a
// Slack login and a state and config directory, all under temporary
// directories.
func newUninstallWorld(t *testing.T) *uninstallWorld {
	t.Helper()
	root, user := tempWorld(t)
	te := newTestEnv(t, &fake.Manager{})
	te.Home = user
	te.noDaemon(t)
	w := &uninstallWorld{testEnv: te, claude: filepath.Join(user, ".claude"), kc: newFakeKeychain(), srv: slacktest.New(t)}
	mkdir(t, w.claude, filepath.Join(user, ".agents"))
	if code := te.run("install"); code != 0 {
		t.Fatalf("install: exit %d: %s", code, te.err)
	}
	for _, a := range []launchAgent{daemonAgent(te.Executable), appAgentOf(bundleExe(user))} {
		path := te.agentPath(a.label)
		writeFile(t, path, te.renderPlist(a, nil))
		w.plists = append(w.plists, path)
	}
	writeFile(t, bundleExe(user), "binary")
	writeFile(t, te.ConfigPath, "{}")
	writeFile(t, filepath.Join(te.StateDir, "store.db"), "store")
	w.profile = filepath.Join(root, "my-profile")
	mkdir(t, w.profile)
	te.cfg.Profile = w.profile
	te.cfg.Slack.KeychainService = "loop-test-slack"
	withSlackPlugin(t, te.Env, (&slackTestEnv{kc: w.kc}).pluginEnv(t, w.srv.URL))
	w.srv.Handle("auth.revoke", func(url.Values) any { return map[string]any{"revoked": true} })
	w.saveSlackToken(t)
	w.sh = fakeLaunchctl(t, te, fakeShell{})
	setInteractive(t, false)
	te.out.Reset()
	te.err.Reset()
	return w
}

func (w *uninstallWorld) saveSlackToken(t *testing.T) {
	t.Helper()
	service := w.cfg.Slack.Service()
	raw, _ := json.Marshal(fakeToken())
	if err := w.kc.Set(service, "current", raw); err != nil {
		t.Fatal(err)
	}
	if err := w.kc.Set(service, "app", []byte("xapp-1-fakeapp")); err != nil {
		t.Fatal(err)
	}
}

func setInteractive(t *testing.T, yes bool) {
	prev := uninstallInteractive
	uninstallInteractive = func(*Env) bool { return yes }
	t.Cleanup(func() { uninstallInteractive = prev })
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func (w *uninstallWorld) skillPaths() []string {
	var out []string
	for _, root := range []string{filepath.Join(w.claude, "skills"), filepath.Join(w.Home, ".agents", "skills")} {
		for _, sk := range bundledSkills() {
			out = append(out, filepath.Join(root, sk.Name, "SKILL.md"))
		}
	}
	return out
}

// assertInstalled fails unless every integration and the data are in place.
func (w *uninstallWorld) assertInstalled(t *testing.T) {
	t.Helper()
	for _, p := range append(append(slices.Clone(w.plists), bundlePath(w.Home), w.StateDir, w.ConfigPath), w.skillPaths()...) {
		if !exists(p) {
			t.Errorf("%s is gone", p)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(w.claude, "settings.json")); !strings.Contains(string(raw), "hook stop") {
		t.Errorf("the hooks are gone: %s", raw)
	}
	if len(w.kc.items) != 2 || len(w.srv.Calls("auth.revoke")) != 0 {
		t.Errorf("Slack: items %d, revokes %d", len(w.kc.items), len(w.srv.Calls("auth.revoke")))
	}
}

func (w *uninstallWorld) assertIntegrationsGone(t *testing.T) {
	t.Helper()
	for _, p := range append(append(slices.Clone(w.plists), bundlePath(w.Home)), w.skillPaths()...) {
		if exists(p) {
			t.Errorf("%s is still there", p)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(w.claude, "settings.json"))
	if strings.Contains(string(raw), "hook stop") || strings.Contains(string(raw), brand.ToolPrefix()) {
		t.Errorf("settings.json keeps entries: %s", raw)
	}
	if mcp, _ := os.ReadFile(filepath.Join(w.claude, ".claude.json")); strings.Contains(string(mcp), `"`+brand.MCPServer()+`"`) {
		t.Errorf("the MCP registration stays: %s", mcp)
	}
	if len(w.kc.items) != 0 || len(w.srv.Calls("auth.revoke")) != 1 {
		t.Errorf("Slack: items %d, revokes %d", len(w.kc.items), len(w.srv.Calls("auth.revoke")))
	}
	for _, label := range []string{launchAgentLabel + ".app", launchAgentLabel} {
		if !slices.Contains(w.sh.launchctl(), "launchctl bootout "+launchDomain()+"/"+label) {
			t.Errorf("no bootout of %s: %q", label, w.sh.launchctl())
		}
	}
}

func TestUninstallTakesOutTheIntegrationsAndKeepsTheData(t *testing.T) {
	w := newUninstallWorld(t)
	if code := w.run("uninstall", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, w.out, w.err)
	}
	w.assertIntegrationsGone(t)
	for _, p := range []string{w.StateDir, w.ConfigPath, w.profile} {
		if !exists(p) {
			t.Errorf("%s is gone without --purge", p)
		}
	}
	out := w.out.String()
	for _, want := range []string{"stopped and removed LaunchAgent " + launchAgentLabel, "removed " + bundlePath(w.Home), "removed skill " + brand.Name + "-review",
		"Deleted the Slack tokens", "brew uninstall --cask " + brand.Name} {
		if !strings.Contains(out, want) {
			t.Errorf("out lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, launchAgentLabel+".app") > strings.Index(out, "LaunchAgent "+launchAgentLabel+" (") {
		t.Errorf("the daemon stopped before the menu bar:\n%s", out)
	}
}

func TestUninstallTwiceFindsNothingLeft(t *testing.T) {
	w := newUninstallWorld(t)
	if code := w.run("uninstall", "--yes", "--purge"); code != 0 {
		t.Fatalf("exit %d: %s", code, w.err)
	}
	w.out.Reset()
	if code := w.run("uninstall", "--purge"); code != 0 {
		t.Fatalf("second run: exit %d: %s", code, w.err)
	}
	if out := w.out.String(); !strings.Contains(out, "Nothing of "+brand.Name+" is left to remove.") || strings.Contains(out, "removed") || strings.Contains(out, "Would") {
		t.Fatalf("second run:\n%s", out)
	}
}

func TestUninstallDryRunChangesNothing(t *testing.T) {
	w := newUninstallWorld(t)
	if code := w.run("uninstall", "--dry-run", "--purge"); code != 0 {
		t.Fatalf("exit %d: %s", code, w.err)
	}
	w.assertInstalled(t)
	if slices.ContainsFunc(w.sh.launchctl(), func(c string) bool { return strings.Contains(c, "bootout") }) {
		t.Fatalf("a dry run stopped a job: %q", w.sh.launchctl())
	}
	out := w.out.String()
	for _, want := range []string{"Would remove:", "LaunchAgent " + launchAgentLabel + " (" + w.plists[0] + ")", "app bundle " + bundlePath(w.Home),
		"remove MCP server " + brand.MCPServer(), "skill " + brand.Name + " (" + filepath.Join(skillsRoot(w.claude), brand.Name) + ")",
		"the Slack tokens, revoked first", "Would delete (--purge):", w.StateDir + ": the store", filepath.Dir(w.ConfigPath) + ": config.toml",
		"Would leave:", "review profile " + w.profile, "brew uninstall --cask " + brand.Name} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Proceed?") {
		t.Errorf("a dry run asked:\n%s", out)
	}
}

func TestUninstallPurgeDeletesTheDataButNotAProfileOutside(t *testing.T) {
	w := newUninstallWorld(t)
	if code := w.run("uninstall", "--purge", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, w.err)
	}
	w.assertIntegrationsGone(t)
	for _, p := range []string{w.StateDir, filepath.Dir(w.ConfigPath)} {
		if exists(p) {
			t.Errorf("%s survived --purge", p)
		}
	}
	if !exists(w.profile) || !strings.Contains(w.out.String(), "review profile "+w.profile) {
		t.Fatalf("profile outside: exists %v\n%s", exists(w.profile), w.out)
	}
}

func TestUninstallPurgeKeepsTheDataWhileADaemonAnswers(t *testing.T) {
	w := newUninstallWorld(t)
	w.serve(t)
	if code := w.run("uninstall", "--purge", "--yes"); code != 1 {
		t.Fatalf("exit %d: %s", code, w.err)
	}
	if !exists(w.StateDir) || !exists(w.ConfigPath) || !strings.Contains(w.err.String(), "still answers") {
		t.Fatalf("state %v, config %v: %s", exists(w.StateDir), exists(w.ConfigPath), w.err)
	}
	w.assertIntegrationsGone(t)
}

func TestUninstallLeavesAChangedSkill(t *testing.T) {
	w := newUninstallWorld(t)
	changed := filepath.Join(skillsRoot(w.claude), brand.Name, "SKILL.md")
	writeFile(t, changed, "mine")
	if code := w.run("uninstall", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, w.err)
	}
	if raw, _ := os.ReadFile(changed); string(raw) != "mine" {
		t.Fatalf("the changed skill holds %q", raw)
	}
	if !strings.Contains(w.out.String(), "skill "+brand.Name+" at "+changed+": "+brand.Name+" did not write it") {
		t.Fatalf("the kept skill went unsaid:\n%s", w.out)
	}
	if exists(filepath.Join(w.claude, "skills", brand.Name+"-review")) {
		t.Fatal("an unchanged skill stayed")
	}
	w.out.Reset()
	w.run("uninstall")
	if out := w.out.String(); !strings.Contains(out, "Nothing of "+brand.Name+" is left") || !strings.Contains(out, brand.Name+" did not write it") {
		t.Fatalf("second run:\n%s", out)
	}
}

func TestUninstallOffATerminalNeedsYes(t *testing.T) {
	for _, args := range [][]string{{"uninstall"}, {"uninstall", "--purge"}} {
		w := newUninstallWorld(t)
		if code := w.run(args...); code != 1 {
			t.Fatalf("%v: exit %d", args, code)
		}
		if !strings.Contains(w.err.String(), "pass --yes") {
			t.Fatalf("%v: %s", args, w.err)
		}
		w.assertInstalled(t)
	}
}

func TestUninstallAsksAtATerminal(t *testing.T) {
	w := newUninstallWorld(t)
	setInteractive(t, true)
	w.In = strings.NewReader("\n")
	if code := w.run("uninstall", "--purge"); code != 1 {
		t.Fatalf("exit %d: %s", code, w.err)
	}
	out := w.out.String()
	for _, want := range []string{"Would remove:", "Would delete (--purge):", w.StateDir, "Proceed? [y/N]", "Nothing removed."} {
		if !strings.Contains(out, want) {
			t.Errorf("out lacks %q:\n%s", want, out)
		}
	}
	w.assertInstalled(t)

	w.In = strings.NewReader("y\n")
	if code := w.run("uninstall"); code != 0 {
		t.Fatalf("exit %d: %s", code, w.err)
	}
	if strings.Contains(w.out.String()[len(out):], "Would delete (--purge):") {
		t.Errorf("the plan without --purge names the data:\n%s", w.out)
	}
	w.assertIntegrationsGone(t)
	if !exists(w.StateDir) {
		t.Error("the data went without --purge")
	}
}
