package e2e

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/wloophq/wloop/internal/brand"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// scrubber rewrites a recording so it names no person, no brand and nothing
// outside the sandbox: the sandbox becomes acme/sandbox in every spelling,
// the viewer me, the home /home/u, every word of the home's base name u,
// the brand loop. allowed are the paths, before the rewrite, a recording may
// hold: the sandbox's checkouts and the agent homes.
type scrubber struct {
	viewer, home string
	allowed      []string
}

var (
	absPath  = regexp.MustCompile(`/(?:Users|home|private|tmp|var|opt|Volumes)(?:/[^"'\s,;)]+)+`)
	repoRef  = regexp.MustCompile(`([\w.-]+/[\w.-]+)#\d+`)
	repoURL  = regexp.MustCompile(`github\.com[/:]([\w.-]+/[\w.-]+?)(?:\.git)?(?:/|"|$)`)
	sandboxR = "acme/sandbox"
)

func (s scrubber) rewrite(text string) string {
	owner, repo, _ := strings.Cut(Sandbox, "/")
	r := strings.NewReplacer(Sandbox, sandboxR, strings.ToLower(Sandbox), sandboxR, s.home, "/home/u", repo, "sandbox", strings.ToLower(repo), "sandbox",
		Prefix, "e2e-", owner, "acme", strings.ToLower(owner), "acme", brand.Name, "loop")
	text = r.Replace(text)
	text = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(owner)+`\b`).ReplaceAllString(text, "acme")
	if !strings.EqualFold(s.viewer, owner) {
		text = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(s.viewer)+`\b`).ReplaceAllString(text, "me")
	}
	for _, p := range nameParts(filepath.Base(s.home)) {
		text = regexp.MustCompile(`(?i)(^|[^a-z0-9])`+regexp.QuoteMeta(p)+`($|[^a-z0-9])`).ReplaceAllString(text, "${1}u${2}")
	}
	return text
}

// text is the recording rewritten, or why it still names something it may
// not.
func (s scrubber) text(raw string) (string, error) {
	out := s.rewrite(raw)
	owner, _, _ := strings.Cut(Sandbox, "/")
	for _, left := range append([]string{s.viewer, owner, s.home, brand.Name}, nameParts(filepath.Base(s.home))...) {
		if strings.Contains(strings.ToLower(out), strings.ToLower(left)) {
			return "", fmt.Errorf("the recording still names %q", left)
		}
	}
	var allowed []string
	for _, a := range s.allowed {
		allowed = append(allowed, s.rewrite(a))
	}
	for _, p := range absPath.FindAllString(out, -1) {
		if !underAny(p, allowed) {
			return "", fmt.Errorf("the recording holds %s, outside the sandbox's checkouts and the agent homes", p)
		}
	}
	for _, re := range []*regexp.Regexp{repoRef, repoURL} {
		for _, m := range re.FindAllStringSubmatch(out, -1) {
			if !strings.EqualFold(m[1], sandboxR) {
				return "", fmt.Errorf("the recording names the repository %s", m[1])
			}
		}
	}
	return out, nil
}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}

// nameParts are the words of a home's base name, lower-cased: an agent
// home spells them hyphenated, dotted or cut at its start.
func nameParts(base string) []string {
	words := strings.FieldsFunc(base, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var out []string
	for _, w := range words {
		if len(w) >= 3 {
			out = append(out, strings.ToLower(w))
		}
	}
	return out
}

// sandboxSessions is the SQL condition of a session of a sandbox
// workspace: its workspace is one, or its worktree lies under a checkout.
func sandboxSessions(snap wsdomain.Snapshot, checkouts []string) string {
	conds := []string{"0"}
	for _, w := range snap.Workspaces {
		conds = append(conds, "workspace_id = "+sqlQuote(w.ID))
	}
	for _, c := range checkouts {
		conds = append(conds, "worktree = "+sqlQuote(c), "worktree LIKE "+sqlQuote(strings.TrimSuffix(c, "/")+"/%"))
	}
	return strings.Join(conds, " OR ")
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// workspaceRoots are the directories the workspace manager keeps the
// sandbox's worktrees in: the parent of each of its workspaces that is not
// a main checkout and is named after the sandbox repository. A worktree
// gone from the listing but still named by a session lies there.
func workspaceRoots(snap wsdomain.Snapshot) []string {
	_, repo, _ := strings.Cut(Sandbox, "/")
	var out []string
	for _, w := range snap.Workspaces {
		dir := filepath.Dir(w.Path)
		if w.IsMain || !strings.EqualFold(filepath.Base(dir), repo) || slices.Contains(out, dir) {
			continue
		}
		out = append(out, dir)
	}
	return out
}

// sandboxRoots are the folders, among the paths raw names, the workspace
// manager keeps the sandbox's worktrees in: a folder of its workspaces
// directory named after the sandbox repository or after the repository's
// last dash-separated word. A worktree gone from the listing lies there.
func sandboxRoots(raw string) []string {
	_, repo, _ := strings.Cut(Sandbox, "/")
	names := []string{strings.ToLower(repo), strings.ToLower(repo[strings.LastIndex(repo, "-")+1:])}
	var out []string
	for _, p := range absPath.FindAllString(raw, -1) {
		for d := filepath.Dir(p); d != "/" && d != "."; d = filepath.Dir(d) {
			if slices.Contains(names, strings.ToLower(filepath.Base(d))) && filepath.Base(filepath.Dir(d)) == "workspaces" && !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	return out
}
