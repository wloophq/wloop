package claude

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/meter"
)

// ImportsQuestion is Claude Code's question whether to allow a CLAUDE.md's
// imports from outside the repository.
const ImportsQuestion = "agent-external-imports"

const importsDialog = "Allow external CLAUDE.md file imports?"

// MCPQuestion is Claude Code's question which servers of a project's
// .mcp.json to enable.
const MCPQuestion = "agent-project-mcp"

// mcpDialog matches the title of that question for one server and for
// several.
var mcpDialog = regexp.MustCompile(`(?i)new MCP servers? found in this project`)

var _ turns.Questions = Agent{}

func (Agent) BlockedOn(screen string) string {
	if strings.Contains(screen, importsDialog) {
		return ImportsQuestion
	}
	if mcpDialog.MatchString(screen) {
		return MCPQuestion
	}
	return ""
}

func (k Agent) Explain(ctx context.Context, s turns.AgentSession, question string) error {
	folder, err := filepath.Abs(s.Worktree)
	if err != nil {
		folder = s.Worktree
	}
	root := repositoryRoot(ctx, folder)
	switch question {
	case turns.QuestionTrust:
		return &turns.UntrustedError{Folder: folder, Home: s.Home, Root: root}
	case ImportsQuestion:
		home := "the agent home of its terminal"
		if s.Home != "" {
			home = "the agent home " + s.Home
		}
		return &turns.QuestionError{Question: question, Root: root,
			Message: "the agent waits on Claude Code's question whether to allow the external CLAUDE.md imports of " + root +
				"; " + brand.Name + " leaves the answer to you: give it once in a Claude Code session there with " + home}
	case MCPQuestion:
		return &turns.QuestionError{Question: question, Root: root,
			Message: "the agent waits on Claude Code's question which MCP servers of the .mcp.json of " + folder +
				" to enable; " + brand.Name + " never answers it: answer it in a Claude Code session there"}
	}
	return nil
}

// Unanswered reads s's .claude.json. The trust question counts as answered
// only once the file records trust for the worktree, its repository root or
// a folder above: the dialog already showed that trust at launch did not
// apply. The imports question counts as answered once the repository
// records it shown or approved, and the MCP question once the settings
// Claude Code reads decide every server of the project's .mcp.json.
func (k Agent) Unanswered(ctx context.Context, s turns.AgentSession, question string) bool {
	switch question {
	case turns.QuestionTrust:
		return !k.recordsTrust(ctx, s)
	case ImportsQuestion:
		if s.Home == "" {
			return true
		}
		f, err := readJSON(claudeJSON(s.Home, k.defaultHome()))
		if err != nil {
			return false
		}
		folder, err := filepath.Abs(s.Worktree)
		if err != nil {
			return false
		}
		projects := object(f.doc, "projects")
		for _, key := range []string{folder, repositoryRoot(ctx, folder)} {
			entry := object(projects, key)
			if entry["hasClaudeMdExternalIncludesWarningShown"] == true || entry["hasClaudeMdExternalIncludesApproved"] == true {
				return false
			}
		}
		return true
	case MCPQuestion:
		return !mcpDecided(ctx, s)
	}
	return false
}

// mcpDecided says whether the settings of s's worktree, of its repository
// root, where Claude Code writes the answer for a worktree, or of s's home
// enable or reject every server of the worktree's .mcp.json, or the root's
// once the worktree is gone. Without either, any server decided counts.
func mcpDecided(ctx context.Context, s turns.AgentSession) bool {
	folder, err := filepath.Abs(s.Worktree)
	if err != nil {
		return false
	}
	dirs := []string{folder}
	if root := repositoryRoot(ctx, folder); root != folder {
		dirs = append(dirs, root)
	}
	var servers map[string]any
	for _, d := range dirs {
		f, err := readJSON(filepath.Join(d, ".mcp.json"))
		if err != nil {
			return false
		}
		if f.exists {
			servers = object(f.doc, "mcpServers")
			break
		}
	}
	var files []string
	for _, d := range dirs {
		files = append(files, filepath.Join(d, ".claude", "settings.local.json"), filepath.Join(d, ".claude", "settings.json"))
	}
	if s.Home != "" {
		files = append(files, settingsFile(s.Home))
	}
	decided := map[string]bool{}
	for _, file := range files {
		f, err := readJSON(file)
		if err != nil {
			continue
		}
		if f.doc["enableAllProjectMcpServers"] == true {
			return true
		}
		for _, key := range []string{"enabledMcpjsonServers", "disabledMcpjsonServers"} {
			names, _ := f.doc[key].([]any)
			for _, n := range names {
				if name, ok := n.(string); ok {
					decided[name] = true
				}
			}
		}
	}
	if servers == nil {
		return len(decided) > 0
	}
	for name := range servers {
		if !decided[name] {
			return false
		}
	}
	return true
}

// Preflight reads the .claude.json a trusted session's home reads and fails
// when Claude Code would ask whether to trust the worktree. It counts as
// trusted the worktree, its main repository root, and any folder above
// either. A home Claude Code never ran in, or one the workspace manager may
// trust the worktree in at launch, is left to the blocked start to tell.
func (k Agent) Preflight(ctx context.Context, s turns.AgentSession) error {
	if !s.Trusted {
		return nil
	}
	u, err := k.untrusted(ctx, s)
	if err != nil || u == nil {
		return err
	}
	return u
}

func (k Agent) untrusted(ctx context.Context, s turns.AgentSession) (*turns.UntrustedError, error) {
	if s.Home == "" || s.Worktree == "" || s.TrustedAtLaunch {
		return nil, nil
	}
	f, err := readJSON(claudeJSON(s.Home, k.defaultHome()))
	if err != nil || len(bytes.TrimSpace(f.raw)) == 0 {
		return nil, err
	}
	worktree, err := filepath.Abs(s.Worktree)
	if err != nil {
		return nil, err
	}
	root := repositoryRoot(ctx, worktree)
	if trustedUnder(f.doc, worktree, root) {
		return nil, nil
	}
	return &turns.UntrustedError{Folder: worktree, Home: s.Home, Root: root}, nil
}

func (k Agent) recordsTrust(ctx context.Context, s turns.AgentSession) bool {
	if s.Home == "" || s.Worktree == "" {
		return false
	}
	f, err := readJSON(claudeJSON(s.Home, k.defaultHome()))
	if err != nil {
		return false
	}
	worktree, err := filepath.Abs(s.Worktree)
	if err != nil {
		return false
	}
	return trustedUnder(f.doc, worktree, repositoryRoot(ctx, worktree))
}

func trustedUnder(doc map[string]any, dirs ...string) bool {
	projects := object(doc, "projects")
	var keys []string
	for _, d := range dirs {
		keys = append(keys, d)
		if real, err := filepath.EvalSymlinks(d); err == nil {
			keys = append(keys, real)
		}
	}
	for _, k := range keys {
		for p := k; ; p = filepath.Dir(p) {
			if object(projects, p)["hasTrustDialogAccepted"] == true {
				return true
			}
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	return false
}

// Trust records the main repository root of folder, or folder itself with
// exact, as trusted in the .claude.json home reads: the entry Claude Code
// writes when the user accepts its trust dialog. Only the user's own
// command calls it.
func (k Agent) Trust(ctx context.Context, folder, home string, exact bool) ([]string, error) {
	folder, err := filepath.Abs(folder)
	if err != nil {
		return nil, err
	}
	key := folder
	if !exact {
		key = repositoryRoot(ctx, folder)
	}
	file := claudeJSON(home, k.defaultHome())
	f, err := readJSON(file)
	if err != nil {
		return nil, err
	}
	if object(object(f.doc, "projects"), key)["hasTrustDialogAccepted"] == true {
		return []string{key + " is already trusted in " + file}, nil
	}
	if err := rewrite(ctx, file, f.raw, trustChange(key)); err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("trusted %s in %s (projects[%q].hasTrustDialogAccepted = true)", key, file, key)}, nil
}

var gitWait = 5 * time.Second

// repositoryRoot is the main checkout of the repository holding dir, the
// key Claude Code files a worktree's project settings under, or dir itself
// outside a repository.
func repositoryRoot(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, gitWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitx.Binary(), "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_WORK_TREE=") || strings.HasPrefix(kv, "GIT_COMMON_DIR=")
	})
	cmd.WaitDelay = time.Second
	start := time.Now()
	out, err := cmd.Output()
	meter.Add(ctx, meter.Sample{Res: meter.Git, Op: "rev-parse", Spawn: true, Err: err != nil && ctx.Err() == nil, Dur: time.Since(start)})
	common := strings.TrimSpace(string(out))
	if err != nil || filepath.Base(common) != ".git" {
		return dir
	}
	return filepath.Dir(common)
}

func trustChange(path string) change {
	return func(doc map[string]any) []string {
		if object(object(doc, "projects"), path)["hasTrustDialogAccepted"] == true {
			return nil
		}
		projectEntry(doc, path)["hasTrustDialogAccepted"] = true
		return []string{"trust " + path}
	}
}

func projectEntry(doc map[string]any, path string) map[string]any {
	projects := object(doc, "projects")
	if projects == nil {
		projects = map[string]any{}
		doc["projects"] = projects
	}
	entry := object(projects, path)
	if entry == nil {
		entry = map[string]any{}
		projects[path] = entry
	}
	return entry
}
