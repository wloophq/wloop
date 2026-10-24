package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
)

func isolate(t *testing.T) string {
	t.Helper()
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	return user
}

func projects(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Projects map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return doc.Projects
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// linkedWorktree returns a worktree added to a new repository and the
// repository's root as git names it.
func linkedWorktree(t *testing.T) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
	return wt, filepath.Dir(gitIn(t, wt, "rev-parse", "--path-format=absolute", "--git-common-dir"))
}

func trustedJSON(paths ...string) string {
	p := map[string]any{}
	for _, path := range paths {
		p[path] = map[string]any{"hasTrustDialogAccepted": true}
	}
	raw, _ := json.Marshal(map[string]any{"numStartups": 3, "projects": p})
	return string(raw)
}

func preflight(t *testing.T, k Agent, s turns.AgentSession) error {
	t.Helper()
	s.Trusted = true
	return k.Preflight(context.Background(), s)
}

func TestPreflightPassesATrustedFolder(t *testing.T) {
	isolate(t)
	wt, root := linkedWorktree(t)
	for name, key := range map[string]string{
		"exact path":                   wt,
		"main repository root":         root,
		"folder above the worktree":    filepath.Dir(wt),
		"folder above the repository":  filepath.Dir(root),
		"worktree through its symlink": mustReal(t, wt),
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			file := filepath.Join(home, ".claude.json")
			body := trustedJSON(key)
			write(t, file, body)
			if err := preflight(t, Agent{}, turns.AgentSession{Worktree: wt, Home: home}); err != nil {
				t.Fatalf("Preflight = %v", err)
			}
			if raw, _ := os.ReadFile(file); string(raw) != body {
				t.Errorf("Preflight changed %s:\n%s", file, raw)
			}
		})
	}
}

func mustReal(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestPreflightFailsAnUntrustedWorktreeAndWritesNothing(t *testing.T) {
	isolate(t)
	wt, root := linkedWorktree(t)
	home := t.TempDir()
	file := filepath.Join(home, ".claude.json")
	body := `{"projects":{"/elsewhere":{"hasTrustDialogAccepted":true},"` + wt + `":{"hasTrustDialogAccepted":false}}}`
	write(t, file, body)
	err := preflight(t, Agent{}, turns.AgentSession{Worktree: wt, Home: home})
	var untrusted *turns.UntrustedError
	if !errors.As(err, &untrusted) {
		t.Fatalf("Preflight = %v, want an UntrustedError", err)
	}
	if untrusted.Folder != wt || untrusted.Home != home || untrusted.Root != root {
		t.Errorf("error = %+v, want folder %s, home %s, root %s", untrusted, wt, home, root)
	}
	if !strings.HasPrefix(err.Error(), "untrusted folder blocks the agent: "+wt+" is not trusted in the agent home "+home) {
		t.Errorf("message = %q", err)
	}
	if raw, _ := os.ReadFile(file); string(raw) != body {
		t.Errorf("Preflight changed %s:\n%s", file, raw)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 1 {
		t.Errorf("left files behind: %v", entries)
	}
}

func TestPreflightLeavesAHomeClaudeNeverRanInToTheBlockedStart(t *testing.T) {
	for name, body := range map[string]*string{"missing": nil, "empty": new(string), "blank": ptr(" \n")} {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			home, wt := t.TempDir(), t.TempDir()
			if body != nil {
				write(t, filepath.Join(home, ".claude.json"), *body)
			}
			if err := preflight(t, Agent{}, turns.AgentSession{Worktree: wt, Home: home}); err != nil {
				t.Fatalf("Preflight = %v", err)
			}
			if body == nil {
				if entries, _ := os.ReadDir(home); len(entries) != 0 {
					t.Errorf("wrote %v", entries)
				}
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestPreflightChecksOnlyATrustedSessionWithAHome(t *testing.T) {
	user := isolate(t)
	wt := t.TempDir()
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"projects":{}}`)
	write(t, filepath.Join(user, ".claude.json"), `{"projects":{}}`)
	if err := (Agent{}).Preflight(context.Background(), turns.AgentSession{Worktree: wt, Home: home}); err != nil {
		t.Errorf("an untrusted pull request's session = %v; its trust dialog is the user's to answer", err)
	}
	if err := preflight(t, Agent{UserHome: user}, turns.AgentSession{Worktree: wt}); err != nil {
		t.Errorf("a session without a home = %v", err)
	}
}

func TestPreflightLeavesAHomeTheWorkspaceManagerTrustsAtLaunch(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"projects":{}}`)
	if err := preflight(t, Agent{}, turns.AgentSession{Worktree: t.TempDir(), Home: home, TrustedAtLaunch: true}); err != nil {
		t.Fatalf("Preflight = %v; the workspace manager may trust the worktree as it launches the session", err)
	}
}

func TestExplainNamesTheRepositoryRootWhileTheWorktreeExists(t *testing.T) {
	isolate(t)
	wt, root := linkedWorktree(t)
	home := t.TempDir()
	var untrusted *turns.UntrustedError
	if err := (Agent{}).Explain(context.Background(), turns.AgentSession{Worktree: wt, Home: home}, turns.QuestionTrust); !errors.As(err, &untrusted) || untrusted.Root != root || untrusted.Folder != wt || untrusted.Home != home {
		t.Fatalf("Explain(trust) = %#v", err)
	}
	var asked *turns.QuestionError
	err := (Agent{}).Explain(context.Background(), turns.AgentSession{Worktree: wt, Home: home}, ImportsQuestion)
	if !errors.As(err, &asked) || asked.Root != root || asked.Question != ImportsQuestion {
		t.Fatalf("Explain(imports) = %#v", err)
	}
	for _, want := range []string{"external CLAUDE.md imports of " + root, "with the agent home " + home} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %s", want, err)
		}
	}
	if err := (Agent{}).Explain(context.Background(), turns.AgentSession{Worktree: wt}, "a dialog"); err != nil {
		t.Errorf("Explain(unknown) = %v", err)
	}
}

func TestUnansweredReadsTheHomeAndWritesNothing(t *testing.T) {
	isolate(t)
	wt, root := linkedWorktree(t)
	for name, c := range map[string]struct {
		body           string
		trust, imports bool
	}{
		"nothing answered":     {`{"projects":{}}`, true, true},
		"root trusted":         {`{"projects":{"` + root + `":{"hasTrustDialogAccepted":true}}}`, false, true},
		"imports shown":        {`{"projects":{"` + root + `":{"hasClaudeMdExternalIncludesWarningShown":true}}}`, true, false},
		"imports approved":     {`{"projects":{"` + root + `":{"hasClaudeMdExternalIncludesApproved":true}}}`, true, false},
		"imports declined set": {`{"projects":{"` + root + `":{"hasClaudeMdExternalIncludesApproved":false,"hasClaudeMdExternalIncludesWarningShown":false}}}`, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			file := filepath.Join(home, ".claude.json")
			write(t, file, c.body)
			s := turns.AgentSession{Worktree: wt, Home: home, Trusted: true}
			if got := (Agent{}).Unanswered(context.Background(), s, turns.QuestionTrust); got != c.trust {
				t.Errorf("Unanswered(trust) = %v", got)
			}
			if got := (Agent{}).Unanswered(context.Background(), s, ImportsQuestion); got != c.imports {
				t.Errorf("Unanswered(imports) = %v", got)
			}
			if raw, _ := os.ReadFile(file); string(raw) != c.body {
				t.Errorf("changed %s:\n%s", file, raw)
			}
		})
	}
}

func TestTheDefaultHomeRunsWithoutCLAUDE_CONFIG_DIRAndReadsTheUsersClaudeJSON(t *testing.T) {
	user := isolate(t)
	home, wt := DefaultHome(user), t.TempDir()
	write(t, filepath.Join(user, ".claude.json"), trustedJSON(wt))
	write(t, filepath.Join(home, ".claude.json"), `{"projects":{}}`)
	k := Agent{UserHome: user}
	s := turns.AgentSession{Worktree: wt, Home: home, Trusted: true}
	got, err := k.Command(s)
	if err != nil || got != "env -u CLAUDE_CONFIG_DIR claude" {
		t.Fatalf("Command = %q, %v", got, err)
	}
	if err := k.Preflight(context.Background(), s); err != nil {
		t.Errorf("Preflight = %v; the user's .claude.json trusts the worktree", err)
	}
	var env []string
	k.Sh = func(_ context.Context, _ string, e []string, _, _ string, _ ...string) ([]byte, error) {
		env = e
		return nil, nil
	}
	if _, err := k.Headless(context.Background(), s, ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(env, []string{"CLAUDE_CONFIG_DIR"}) {
		t.Errorf("headless env = %q, want CLAUDE_CONFIG_DIR unset", env)
	}
}

func TestAnotherHomeRunsWithItsConfigDirAndReadsItsClaudeJSON(t *testing.T) {
	user := isolate(t)
	home, wt := filepath.Join(user, ".claude-personal"), t.TempDir()
	write(t, filepath.Join(user, ".claude.json"), trustedJSON(wt))
	write(t, filepath.Join(home, ".claude.json"), `{"projects":{}}`)
	k := Agent{UserHome: user}
	s := turns.AgentSession{Worktree: wt, Home: home, Trusted: true}
	if got, err := k.Command(s); err != nil || got != "CLAUDE_CONFIG_DIR='"+home+"' claude" {
		t.Fatalf("Command = %q, %v", got, err)
	}
	if err := k.Preflight(context.Background(), s); !errors.As(err, new(*turns.UntrustedError)) {
		t.Errorf("Preflight = %v; only the user's default .claude.json trusts the worktree", err)
	}
}

func TestBlockedOnTellsTheExternalImportsDialog(t *testing.T) {
	screen := "  External CLAUDE.md imports\n\n  Allow external CLAUDE.md file imports?\n  > 1. Yes, allow external imports\n    2. No, disable external imports"
	if got := (Agent{}).BlockedOn(screen); got != ImportsQuestion {
		t.Errorf("BlockedOn = %q, want %q", got, ImportsQuestion)
	}
	if got := (Agent{}).BlockedOn("> try \"fix the tests\""); got != "" {
		t.Errorf("BlockedOn an idle prompt = %q", got)
	}
}

func TestTrustRecordsTheMainRepositoryRoot(t *testing.T) {
	isolate(t)
	wt, root := linkedWorktree(t)
	home := t.TempDir()
	file := filepath.Join(home, ".claude.json")
	write(t, file, `{"numStartups":12,"oauthAccount":{"emailAddress":"me@x"},"projects":{"/other":{"hasTrustDialogAccepted":true,"allowedTools":["Bash"]},"`+root+`":{"allowedTools":["Read"],"hasTrustDialogAccepted":false}}}`)
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := (Agent{}).Trust(context.Background(), wt, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "trusted "+root+" in "+file) {
		t.Errorf("lines = %q", lines)
	}
	got := projects(t, file)
	if got[root]["hasTrustDialogAccepted"] != true || got[root]["allowedTools"] == nil {
		t.Fatalf("root entry = %v", got[root])
	}
	if _, ok := got[wt]; ok {
		t.Errorf("trusted the worktree key too: %v", got[wt])
	}
	for key, entry := range got {
		if _, ok := entry["hasClaudeMdExternalIncludesWarningShown"]; ok {
			t.Errorf("answered the imports warning for %s", key)
		}
	}
	if got["/other"]["hasTrustDialogAccepted"] != true || got["/other"]["allowedTools"] == nil {
		t.Errorf("another project changed: %v", got["/other"])
	}
	raw, _ := os.ReadFile(file)
	for _, want := range []string{`"numStartups": 12`, `"emailAddress": "me@x"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("lost %s:\n%s", want, raw)
		}
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(home); len(entries) != 1 {
		t.Errorf("left files behind: %v", entries)
	}
	if err := preflight(t, Agent{}, turns.AgentSession{Worktree: wt, Home: home}); err != nil {
		t.Errorf("Preflight after Trust = %v", err)
	}
}

func TestTrustExactRecordsTheFolderItself(t *testing.T) {
	isolate(t)
	wt, root := linkedWorktree(t)
	home := t.TempDir()
	file := filepath.Join(home, ".claude.json")
	write(t, file, `{}`)
	if _, err := (Agent{}).Trust(context.Background(), wt, home, true); err != nil {
		t.Fatal(err)
	}
	got := projects(t, file)
	if len(got) != 1 || got[wt]["hasTrustDialogAccepted"] != true {
		t.Fatalf("projects = %v, want only %s (not %s)", got, wt, root)
	}
}

func TestTrustLeavesATrustedFolderUntouched(t *testing.T) {
	isolate(t)
	home, dir := t.TempDir(), t.TempDir()
	file := filepath.Join(home, ".claude.json")
	body := `{"projects":{"` + dir + `":{"hasTrustDialogAccepted":true,"lastCost":1.50}}}`
	write(t, file, body)
	lines, err := (Agent{}).Trust(context.Background(), dir, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "already trusted") {
		t.Errorf("lines = %q", lines)
	}
	if raw, _ := os.ReadFile(file); string(raw) != body {
		t.Fatalf("rewrote a trusted folder:\n%s", raw)
	}
}

func TestTrustWritesTheUsersClaudeJSONForTheDefaultHome(t *testing.T) {
	user := isolate(t)
	dir := t.TempDir()
	write(t, filepath.Join(user, ".claude.json"), `{}`)
	if _, err := (Agent{UserHome: user}).Trust(context.Background(), dir, DefaultHome(user), false); err != nil {
		t.Fatal(err)
	}
	if projects(t, filepath.Join(user, ".claude.json"))[dir]["hasTrustDialogAccepted"] != true {
		t.Error("the user's .claude.json does not trust the folder")
	}
}

func TestTrustWritesThroughASymlinkedClaudeJSON(t *testing.T) {
	isolate(t)
	home, dir := t.TempDir(), t.TempDir()
	target := filepath.Join(t.TempDir(), "claude.json")
	write(t, target, `{"theme":"dark"}`)
	link := filepath.Join(home, ".claude.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := (Agent{}).Trust(context.Background(), dir, home, false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v", err)
	}
	if projects(t, target)[dir]["hasTrustDialogAccepted"] != true {
		t.Fatal("not trusted through the link")
	}
}

func TestTrustWaitsForClaudesLock(t *testing.T) {
	isolate(t)
	home, dir := t.TempDir(), t.TempDir()
	file := filepath.Join(home, ".claude.json")
	write(t, file, `{}`)
	lock := file + ".lock"
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := (Agent{}).Trust(context.Background(), dir, home, false)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("wrote under a held lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if projects(t, file) != nil {
		t.Fatal("the file changed under a held lock")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if projects(t, file)[dir]["hasTrustDialogAccepted"] != true {
		t.Fatal("not trusted after the lock was released")
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("left the lock: %v", err)
	}
}

func TestTrustBreaksAStaleLock(t *testing.T) {
	isolate(t)
	home, dir := t.TempDir(), t.TempDir()
	file := filepath.Join(home, ".claude.json")
	write(t, file, `{}`)
	lock := file + ".lock"
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := (Agent{}).Trust(context.Background(), dir, home, false); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second || projects(t, file)[dir]["hasTrustDialogAccepted"] != true {
		t.Fatalf("stale lock not broken in time (%v)", time.Since(start))
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("left the lock: %v", err)
	}
}

func TestTrustLocksTheLinkPath(t *testing.T) {
	isolate(t)
	home, dir := t.TempDir(), t.TempDir()
	target := filepath.Join(t.TempDir(), "claude.json")
	write(t, target, `{}`)
	link := filepath.Join(home, ".claude.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(link+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := (Agent{}).Trust(ctx, dir, home, false); err == nil {
		t.Fatal("wrote while the link path was locked")
	}
}

func TestTrustKeepsAConcurrentChange(t *testing.T) {
	isolate(t)
	file := filepath.Join(t.TempDir(), ".claude.json")
	write(t, file, `{"numStartups":1}`)
	raw, _ := os.ReadFile(file)
	changed := false
	c := func(doc map[string]any) []string {
		out := trustChange("/w")(doc)
		if !changed {
			changed = true
			write(t, file, `{"numStartups":2,"projects":{"/x":{"hasTrustDialogAccepted":true}}}`)
		}
		return out
	}
	if err := rewrite(context.Background(), file, raw, c); err != nil {
		t.Fatal(err)
	}
	got := projects(t, file)
	if got["/w"]["hasTrustDialogAccepted"] != true || got["/x"]["hasTrustDialogAccepted"] != true {
		t.Fatalf("projects = %v", got)
	}
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), `"numStartups": 2`) {
		t.Fatalf("lost the concurrent change:\n%s", b)
	}
}

func TestAStaleLockThatCannotBeBrokenFailsInTime(t *testing.T) {
	isolate(t)
	home, dir := t.TempDir(), t.TempDir()
	target := filepath.Join(t.TempDir(), "claude.json")
	write(t, target, `{}`)
	link := filepath.Join(home, ".claude.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	lock := link + ".lock"
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(home, 0o755) })
	saved := lockWait
	lockWait = 300 * time.Millisecond
	t.Cleanup(func() { lockWait = saved })

	start := time.Now()
	if _, err := (Agent{}).Trust(context.Background(), dir, home, false); err == nil {
		t.Fatal("wrote past a lock it could not break")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v past a 300ms deadline", d)
	}

	lockWait = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := (Agent{}).Trust(ctx, dir, home, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's deadline", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("ignored the context for %v", d)
	}
}

func TestUnlockLeavesALockThatIsNotOurs(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), ".claude.json")
	unlock, err := lockFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("removed another holder's lock: %v", err)
	}
}

func TestAHungGitFallsBackToTheWorkdir(t *testing.T) {
	isolate(t)
	bin := t.TempDir()
	write(t, filepath.Join(bin, "git"), "#!/bin/sh\nsleep 30\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	defer func(w time.Duration) { gitWait = w }(gitWait)
	gitWait = 200 * time.Millisecond
	dir := t.TempDir()
	start := time.Now()
	if got := repositoryRoot(context.Background(), dir); got != dir {
		t.Errorf("repositoryRoot = %q, want %q", got, dir)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %v", took)
	}
}

func TestBlockedOnTellsTheProjectMCPDialog(t *testing.T) {
	for _, screen := range []string{
		"  3 new MCP servers found in this project\n  Select any you wish to enable.\n\n  ❯ [✔] playwright",
		"  New MCP server found in this project: grafana\n  ❯ 1. Use this MCP server",
	} {
		if got := (Agent{}).BlockedOn(screen); got != MCPQuestion {
			t.Errorf("BlockedOn(%q) = %q, want %q", screen, got, MCPQuestion)
		}
	}
}

func TestExplainNamesTheProjectMCPServersToAnswer(t *testing.T) {
	isolate(t)
	wt, root := linkedWorktree(t)
	var asked *turns.QuestionError
	err := (Agent{}).Explain(context.Background(), turns.AgentSession{Worktree: wt, Home: t.TempDir()}, MCPQuestion)
	if !errors.As(err, &asked) || asked.Root != root || asked.Question != MCPQuestion {
		t.Fatalf("Explain(mcp) = %#v", err)
	}
	for _, want := range []string{"which MCP servers of the .mcp.json of " + wt + " to enable", "answer it in a Claude Code session there"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %s", want, err)
		}
	}
}

func TestUnansweredMCPReadsTheProjectLocalSettings(t *testing.T) {
	const servers = `{"mcpServers":{"playwright":{},"grafana":{}}}`
	for name, c := range map[string]struct {
		at, settings string
		home         string
		want         bool
	}{
		"nothing answered":          {want: true},
		"one server left":           {at: "root", settings: `{"enabledMcpjsonServers":["playwright"]}`, want: true},
		"all enabled at the root":   {at: "root", settings: `{"enabledMcpjsonServers":["playwright","grafana"]}`},
		"enabled and rejected":      {at: "root", settings: `{"enabledMcpjsonServers":["playwright"],"disabledMcpjsonServers":["grafana"]}`},
		"all enabled at the folder": {at: "folder", settings: `{"enabledMcpjsonServers":["playwright","grafana"]}`},
		"every project server":      {at: "root", settings: `{"enableAllProjectMcpServers":true}`},
		"the home's settings":       {home: `{"enableAllProjectMcpServers":true}`},
	} {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			wt, root := linkedWorktree(t)
			write(t, filepath.Join(wt, ".mcp.json"), servers)
			home := t.TempDir()
			switch c.at {
			case "root":
				write(t, filepath.Join(root, ".claude", "settings.local.json"), c.settings)
			case "folder":
				write(t, filepath.Join(wt, ".claude", "settings.local.json"), c.settings)
			}
			if c.home != "" {
				write(t, filepath.Join(home, "settings.json"), c.home)
			}
			if got := (Agent{}).Unanswered(context.Background(), turns.AgentSession{Worktree: wt, Home: home}, MCPQuestion); got != c.want {
				t.Errorf("Unanswered(mcp) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUnansweredMCPOfARemovedWorktreeReadsTheRootsServers(t *testing.T) {
	isolate(t)
	wt, root := linkedWorktree(t)
	write(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"playwright":{}}}`)
	if !(Agent{}).Unanswered(context.Background(), turns.AgentSession{Worktree: root}, MCPQuestion) {
		t.Fatal("answered with no setting")
	}
	write(t, filepath.Join(root, ".claude", "settings.local.json"), `{"disabledMcpjsonServers":["playwright"]}`)
	if (Agent{}).Unanswered(context.Background(), turns.AgentSession{Worktree: root}, MCPQuestion) {
		t.Fatalf("unanswered after rejecting every server of %s (worktree %s)", root, wt)
	}
}

func TestUnansweredMCPWithoutAReadableMCPJSONWaitsForAnyAnswer(t *testing.T) {
	isolate(t)
	gone := filepath.Join(t.TempDir(), "gone")
	if !(Agent{}).Unanswered(context.Background(), turns.AgentSession{Worktree: gone}, MCPQuestion) {
		t.Fatal("answered with neither a .mcp.json nor a setting")
	}
	write(t, filepath.Join(gone, ".claude", "settings.local.json"), `{"enabledMcpjsonServers":["playwright"]}`)
	if (Agent{}).Unanswered(context.Background(), turns.AgentSession{Worktree: gone}, MCPQuestion) {
		t.Fatal("unanswered though the settings record an answer")
	}
}
