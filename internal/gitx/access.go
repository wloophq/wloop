package gitx

import (
	"bufio"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Access is the outcome of one git operation that talked to a remote host.
type Access struct {
	Host string
	At   time.Time
	// Op names the operation and its subject, such as "fetch of acme/widget#7".
	Op  string
	Err string
}

// Observed runs git through R and reports every network operation's outcome
// to Observe; a run its context cancelled reports nothing.
type Observed struct {
	R       Runner
	Observe func(Access)
	Now     func() time.Time
}

func (o Observed) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	out, err := o.R.Run(ctx, dir, env, args...)
	if len(args) == 0 || (args[0] != "fetch" && args[0] != "ls-remote") || ctx.Err() != nil {
		return out, err
	}
	positional := operands(args[1:])
	remote := "origin"
	if len(positional) > 0 {
		remote = positional[0]
	}
	remoteURL := remote
	if !looksLikeURL(remote) {
		remoteURL = RemoteURL(dir, remote)
	}
	host, repo := ParseRemote(remoteURL)
	if host == "" {
		return out, err
	}
	a := Access{Host: host, At: o.Now(), Op: subject(args[0], repo, positional)}
	if err != nil {
		a.Err = err.Error()
	}
	o.Observe(a)
	return out, err
}

func operands(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

var (
	pullSpec   = regexp.MustCompile(`refs/pull/(\d+)/head`)
	branchSpec = regexp.MustCompile(`refs/heads/([^:]+)`)
)

func subject(op, repo string, positional []string) string {
	s := op + " of " + repo
	if op != "fetch" || len(positional) < 2 {
		return s
	}
	if m := pullSpec.FindStringSubmatch(positional[1]); m != nil {
		return s + "#" + m[1]
	}
	if m := branchSpec.FindStringSubmatch(positional[1]); m != nil {
		return s + " (" + m[1] + ")"
	}
	return s
}

func looksLikeURL(remote string) bool {
	return strings.Contains(remote, "://") || strings.Contains(remote, "@")
}

var scpLike = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.*)$`)

// ParseRemote is the host and the repository path, without ".git", of a
// remote URL: scp-like (git@github.com:acme/widget.git), ssh:// or https://.
// Both are empty for a local path.
func ParseRemote(remote string) (host, repo string) {
	remote = strings.TrimSpace(remote)
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.Scheme == "file" {
			return "", ""
		}
		return strings.ToLower(u.Hostname()), cleanRepo(u.Path)
	}
	if m := scpLike.FindStringSubmatch(remote); m != nil {
		return strings.ToLower(m[1]), cleanRepo(m[2])
	}
	return "", ""
}

func cleanRepo(p string) string {
	return strings.TrimSuffix(strings.Trim(p, "/"), ".git")
}

// RemoteURL is the url of remote in the repository at dir, read from its
// config file so recording a fetch spawns no git process; "" when unset.
func RemoteURL(dir, remote string) string {
	f, err := os.Open(filepath.Join(commonDir(dir), "config"))
	if err != nil {
		return ""
	}
	defer f.Close()
	section := `[remote "` + remote + `"]`
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = line == section
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if in && ok && strings.TrimSpace(key) == "url" {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

// commonDir is the git directory a checkout at dir shares with its other
// worktrees.
func commonDir(dir string) string {
	gitDir := filepath.Join(dir, ".git")
	if b, err := os.ReadFile(gitDir); err == nil {
		gitDir = strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(dir, gitDir)
		}
	}
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimSpace(string(b))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		gitDir = common
	}
	return gitDir
}

var promptProblems = []string{
	"permission denied (publickey",
	"permission denied, please try again",
	"agent refused operation",
	"could not read username",
	"could not read password",
	"terminal prompts disabled",
	"host key verification failed",
	"passphrase",
	"authentication failed",
}

// NeedsPrompt reports whether a git or ssh error means the access needs a
// credential, a key or a host key the process could not get without asking.
func NeedsPrompt(msg string) bool {
	msg = strings.ToLower(msg)
	for _, p := range promptProblems {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
