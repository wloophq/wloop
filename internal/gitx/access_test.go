package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseRemote(t *testing.T) {
	for in, want := range map[string][2]string{
		"git@github.com:acme/widget.git":               {"github.com", "acme/widget"},
		"https://github.com/acme/widget":               {"github.com", "acme/widget"},
		"https://token@GitHub.com/acme/widget.git":     {"github.com", "acme/widget"},
		"ssh://git@github.example.com:2222/acme/w.git": {"github.example.com", "acme/w"},
		"github.com:acme/widget":                       {"github.com", "acme/widget"},
		"/srv/git/widget.git":                          {"", ""},
		"file:///srv/git/widget.git":                   {"", ""},
		"":                                             {"", ""},
	} {
		host, repo := ParseRemote(in)
		if host != want[0] || repo != want[1] {
			t.Errorf("ParseRemote(%q) = %q, %q, want %q, %q", in, host, repo, want[0], want[1])
		}
	}
}

func TestNeedsPrompt(t *testing.T) {
	for msg, want := range map[string]bool{
		"git@github.com: Permission denied (publickey).":                                           true,
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled":       true,
		"Host key verification failed.":                                                            true,
		"Enter passphrase for key '/Users/me/.ssh/id_ed25519':":                                    true,
		"Permission denied, please try again.":                                                     true,
		"sign_and_send_pubkey: signing failed: agent refused operation":                            true,
		"error: cannot open .git/FETCH_HEAD: Permission denied":                                    false,
		"ssh: Could not resolve hostname github.com: nodename nor servname provided":               false,
		"ssh: connect to host github.com port 22: Operation timed out":                             false,
		"remote: Internal Server Error":                                                            false,
		"fatal: unable to access 'https://github.com/a/b/': The requested URL returned error: 502": false,
	} {
		if got := NeedsPrompt(msg); got != want {
			t.Errorf("NeedsPrompt(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestRemoteURLReadsTheSharedConfig(t *testing.T) {
	main := t.TempDir()
	writeFile(t, filepath.Join(main, ".git", "config"), "[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = https://github.com/up/widget\n[remote \"origin\"]\n\turl = git@github.com:acme/widget.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n")
	linked := t.TempDir()
	writeFile(t, filepath.Join(main, ".git", "worktrees", "wt", "commondir"), "../..\n")
	writeFile(t, filepath.Join(linked, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n")
	if got := RemoteURL(linked, "origin"); got != "git@github.com:acme/widget.git" {
		t.Errorf("origin = %q", got)
	}
	if got := RemoteURL(main, "upstream"); got != "https://github.com/up/widget" {
		t.Errorf("upstream = %q", got)
	}
	if got := RemoteURL(main, "absent"); got != "" {
		t.Errorf("absent = %q", got)
	}
}

func TestObservedReportsNetworkOperationsOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/widget.git\n")
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var seen []Access
	g := Git{R: Observed{
		R: cannedGit{
			"fetch":     {err: &Error{Args: []string{"fetch"}, Msg: "git@github.com: Permission denied (publickey).", Code: 128}},
			"ls-remote": {out: "c0ffee\trefs/heads/main\n"},
		},
		Observe: func(a Access) { seen = append(seen, a) },
		Now:     func() time.Time { return at },
	}}
	ctx := context.Background()
	_ = g.FetchPR(ctx, dir, 4593)
	_, _ = g.RemoteHasBranch(ctx, dir, "origin", "main")
	_, _ = g.TrackBranch(ctx, dir, "feature")
	_, _ = g.Head(ctx, dir)
	want := []Access{
		{Host: "github.com", At: at, Op: "fetch of acme/widget#4593", Err: "git fetch: git@github.com: Permission denied (publickey)."},
		{Host: "github.com", At: at, Op: "ls-remote of acme/widget"},
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("seen %+v\nwant %+v", seen, want)
	}
}

func TestObservedIgnoresACancelledRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "config"), "[remote \"origin\"]\n\turl = https://github.com/acme/widget\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := Git{R: Observed{R: cannedGit{"fetch": {err: errors.New("signal: killed")}}, Observe: func(a Access) { t.Errorf("observed %+v", a) }, Now: time.Now}}
	_ = g.FetchPR(ctx, dir, 1)
}
