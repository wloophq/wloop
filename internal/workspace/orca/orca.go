// Package orca implements workspace.Manager over Orca's runtime socket and the orca CLI.
package orca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/workspace"
)

type Runner interface {
	// Run returns stdout even when the command exits non-zero: orca reports
	// failures inside its JSON envelope.
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type Exec struct{ Bin string }

func (e Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin := e.Bin
	if bin == "" {
		bin = "orca"
	}
	if bin == "orca" && testing.Testing() && os.Getenv(LiveVar) != "1" {
		return nil, fmt.Errorf("orca %s: %w", strings.Join(args, " "), errExecInTest)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && stdout.Len() == 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("orca %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// errExecInTest keeps a test binary off the user's orca.
var errExecInTest = fmt.Errorf("%w: a test may not run the orca binary: set Exec.Bin, or %s=1", workspace.ErrUnreachable, LiveVar)

type Manager struct {
	R Runner
	// RPC carries every call that has a runtime method; without it every call goes
	// through the CLI, and workspaces from a pull request keep Orca's generated branch name.
	RPC RPC
	// Identities maps an agent's name to the agent identity Orca
	// reports for its terminals; a kind missing from it is its own identity.
	Identities map[string]string
	// DefaultBranch reads a local checkout's default branch; nil reads origin/HEAD.
	DefaultBranch func(path string) string

	fallbacks atomic.Int64
	branches  sync.Map
}

func New() *Manager {
	return &Manager{R: MeteredRunner{Next: Exec{}}, RPC: MeteredRPC{Next: &Runtime{}}}
}

func (m *Manager) Name() string { return "orca" }

// Hosts is true for every agent: Orca runs any command in a terminal.
func (m *Manager) Hosts(string) bool { return true }

var (
	_ workspace.Manager         = (*Manager)(nil)
	_ workspace.Observer        = (*Manager)(nil)
	_ workspace.AgentIdentifier = (*Manager)(nil)
	_ workspace.Board           = (*Manager)(nil)
	_ workspace.Linker          = (*Manager)(nil)
	_ workspace.Renamer         = (*Manager)(nil)
	_ workspace.ScreenReader    = (*Manager)(nil)
	_ workspace.TerminalCloser  = (*Manager)(nil)
	_ workspace.LaunchTruster   = (*Manager)(nil)
	_ workspace.Waker           = (*Manager)(nil)
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
	Meta   struct {
		RuntimeID string `json:"runtimeId"`
	} `json:"_meta"`
}

type orcaError struct {
	args []string
	code string
	msg  string
}

// Detail is Orca's own message, without the command.
func (e *orcaError) Detail() string { return e.msg }

func (e *orcaError) Error() string {
	return fmt.Sprintf("orca %s: %s", strings.Join(e.args, " "), e.msg)
}

// decode reads an Orca envelope into out and returns the runtime id it carries.
func decode(raw []byte, out any, args []string) (string, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("orca %s: unexpected output: %.200s", strings.Join(args, " "), raw)
	}
	if !env.OK {
		return env.Meta.RuntimeID, &orcaError{args: args, code: errorCode(env.Error), msg: errorText(env.Error)}
	}
	if out == nil {
		return env.Meta.RuntimeID, nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return "", fmt.Errorf("orca %s: parsing result: %w", strings.Join(args, " "), err)
	}
	return env.Meta.RuntimeID, nil
}

func errorCode(raw json.RawMessage) string {
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &e)
	return e.Code
}

func errorText(raw json.RawMessage) string {
	var e struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		if e.Code != "" {
			return e.Code + ": " + e.Message
		}
		return e.Message
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func (m *Manager) Reachable(ctx context.Context) error {
	var res struct {
		GraphStatus string `json:"graphStatus"`
		Runtime     struct {
			Reachable bool   `json:"reachable"`
			State     string `json:"state"`
		} `json:"runtime"`
	}
	if err := m.do(ctx, &res, statusOp()); err != nil {
		return err
	}
	if res.GraphStatus == "" {
		if !res.Runtime.Reachable || res.Runtime.State != "ready" {
			return fmt.Errorf("orca runtime not ready (state %q)", res.Runtime.State)
		}
		return nil
	}
	if res.GraphStatus != "ready" {
		return fmt.Errorf("orca runtime not ready (graph %q)", res.GraphStatus)
	}
	return nil
}

type repoRow struct {
	ID                string `json:"id"`
	Path              string `json:"path"`
	ExecutionHostID   string `json:"executionHostId"`
	ConnectionID      string `json:"connectionId"`
	GitRemoteIdentity *struct {
		CanonicalKey string `json:"canonicalKey"`
	} `json:"gitRemoteIdentity"`
}

func (m *Manager) repo(r repoRow) wsdomain.Repo {
	out := wsdomain.Repo{ID: r.ID, Path: r.Path}
	if (r.ExecutionHostID == "" || r.ExecutionHostID == wsdomain.LocalHost) && r.ConnectionID == "" {
		out.DefaultBranch = m.defaultBranch(r.Path)
	}
	if r.GitRemoteIdentity != nil {
		out.RemoteKey = r.GitRemoteIdentity.CanonicalKey
	}
	return out
}

// defaultBranch keeps a known default branch for the manager's life and reads an unknown
// one again on each load: an unreadable checkout or a missing origin/HEAD may recover.
func (m *Manager) defaultBranch(path string) string {
	if b, ok := m.branches.Load(path); ok {
		return b.(string)
	}
	read := m.DefaultBranch
	if read == nil {
		read = gitx.DefaultBranch
	}
	b := read(path)
	if b != "" {
		m.branches.Store(path, b)
	}
	return b
}

func (m *Manager) Repos(ctx context.Context) ([]wsdomain.Repo, error) {
	var res struct {
		Repos []repoRow `json:"repos"`
	}
	if err := m.do(ctx, &res, repoListOp()); err != nil {
		return nil, err
	}
	out := make([]wsdomain.Repo, 0, len(res.Repos))
	for _, r := range res.Repos {
		out = append(out, m.repo(r))
	}
	return out, nil
}

func (m *Manager) AddRepo(ctx context.Context, path string) (wsdomain.Repo, error) {
	var res struct {
		Repo repoRow `json:"repo"`
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return wsdomain.Repo{}, err
	}
	if err := m.do(ctx, &res, repoAddOp(path, abs)); err != nil {
		return wsdomain.Repo{}, err
	}
	return m.repo(res.Repo), nil
}

type worktreeRow struct {
	ID              string          `json:"id"`
	RepoID          string          `json:"repoId"`
	Path            string          `json:"path"`
	DisplayName     string          `json:"displayName"`
	Branch          string          `json:"branch"`
	WorkspaceStatus string          `json:"workspaceStatus"`
	HostID          string          `json:"hostId"`
	LinkedPR        json.RawMessage `json:"linkedPR"`
	IsMainWorktree  bool            `json:"isMainWorktree"`
	Comment         string          `json:"comment"`
	IsUnread        bool            `json:"isUnread"`
	PushTarget      *struct {
		BranchName string `json:"branchName"`
	} `json:"pushTarget"`
}

func (w worktreeRow) workspace() wsdomain.Workspace {
	out := wsdomain.Workspace{
		ID:          w.ID,
		RepoID:      w.RepoID,
		Path:        w.Path,
		DisplayName: w.DisplayName,
		Branch:      strings.TrimPrefix(w.Branch, "refs/heads/"),
		Status:      w.WorkspaceStatus,
		HostID:      w.HostID,
		LinkedPR:    linkedPR(w.LinkedPR),
		IsMain:      w.IsMainWorktree,
		Comment:     w.Comment,
		Unread:      w.IsUnread,
	}
	if out.HostID == "" {
		out.HostID = wsdomain.LocalHost
	}
	if w.PushTarget != nil {
		out.PushBranch = w.PushTarget.BranchName
	}
	return out
}

// linkedPR accepts both shapes orca uses: a bare number in `worktree list`
// and {number, state} in `worktree ps`.
func linkedPR(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var obj struct {
		Number int `json:"number"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Number
	}
	return 0
}

// hostScope is the host coverage Orca reports on a listing.
type hostScope struct {
	HostIDs        []string `json:"hostIds"`
	OmittedHostIDs []string `json:"omittedHostIds"`
}

// coverage is what a listing says about its own completeness; a listing that leaves
// either field out is incomplete.
type coverage struct {
	Truncated *bool      `json:"truncated"`
	HostScope *hostScope `json:"hostScope"`
}

func (c coverage) scope(runtimeID string) wsdomain.Scope {
	if c.Truncated == nil || c.HostScope == nil {
		return wsdomain.Scope{RuntimeID: runtimeID}
	}
	return wsdomain.Scope{Hosts: c.HostScope.HostIDs, Complete: !*c.Truncated && len(c.HostScope.OmittedHostIDs) == 0, RuntimeID: runtimeID}
}

func (m *Manager) List(ctx context.Context) ([]wsdomain.Workspace, error) {
	out, _, err := m.Workspaces(ctx)
	return out, err
}

func (m *Manager) Workspaces(ctx context.Context) ([]wsdomain.Workspace, wsdomain.Scope, error) {
	var res struct {
		Worktrees []worktreeRow `json:"worktrees"`
		coverage
	}
	runtimeID, err := m.doMeta(ctx, &res, worktreeListOp())
	if err != nil {
		return nil, wsdomain.Scope{}, err
	}
	out := make([]wsdomain.Workspace, 0, len(res.Worktrees))
	for _, w := range res.Worktrees {
		out = append(out, w.workspace())
	}
	return out, res.scope(runtimeID), nil
}

func (m *Manager) Create(ctx context.Context, req workspace.CreateRequest) (wsdomain.Workspace, string, error) {
	ws, handle, err := m.create(ctx, req)
	ws.BranchRenamed = renamedBranch(ws.Branch, req.Checkout.Branch)
	return ws, handle, err
}

// renamedBranch reports whether got is want with Orca's "-<n>" suffix.
func renamedBranch(got, want string) bool {
	n, ok := strings.CutPrefix(got, want+"-")
	return ok && want != "" && n != "" && strings.Trim(n, "0123456789") == ""
}

func (m *Manager) create(ctx context.Context, req workspace.CreateRequest) (wsdomain.Workspace, string, error) {
	if req.Checkout.Branch != "" && m.RPC != nil {
		if ws, handle, err := m.createFromPR(ctx, req); err == nil {
			ws.ParentDropped = req.Parent != ""
			return ws, handle, nil
		}
	}
	args := []string{"worktree", "create", "--repo", "id:" + req.RepoID, "--name", req.Name}
	parent, listed := m.listedWorktree(ctx, req.Parent)
	if listed {
		args = append(args, "--parent-worktree", "path:"+parent)
	} else {
		args = append(args, "--no-parent")
	}
	if req.BaseRef != "" && !req.DefaultBase {
		args = append(args, "--base-branch", req.BaseRef)
	}
	if n := workspace.LinkedPR(req.Unit); n != 0 {
		args = append(args, "--pr", strconv.Itoa(n))
	}
	if req.Agent != "" {
		args = append(args, "--agent", req.Agent)
	}
	if req.Prompt != "" {
		args = append(args, "--prompt", req.Prompt)
	}
	var res struct {
		Worktree            worktreeRow `json:"worktree"`
		AgentTerminalHandle string      `json:"agentTerminalHandle"`
	}
	if err := m.do(ctx, &res, cliOp(args...)); err != nil {
		return wsdomain.Workspace{}, "", err
	}
	ws := res.Worktree.workspace()
	ws.ParentDropped = req.Parent != "" && !listed
	return ws, res.AgentTerminalHandle, nil
}

// listedWorktree is the path of the worktree Orca lists at path; Orca refuses
// a parent selector it cannot resolve.
func (m *Manager) listedWorktree(ctx context.Context, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	path = filepath.Clean(path)
	list, _, err := m.Workspaces(ctx)
	if err != nil {
		return "", false
	}
	for _, w := range list {
		if w.Path != "" && filepath.Clean(w.Path) == path {
			return w.Path, true
		}
	}
	return "", false
}

func (m *Manager) SetStatus(ctx context.Context, id, status string) error {
	err := m.do(ctx, nil, worktreeSetOp(id, card{status: status}))
	var oe *orcaError
	if errors.As(err, &oe) {
		return fmt.Errorf("%w %q: %s", workspace.ErrUnknownStatus, status, oe.msg)
	}
	return err
}

// SetCard writes the card's column, comment and unread in one call; an empty
// status or comment and a false unread leave that part as it is.
func (m *Manager) SetCard(ctx context.Context, id, status, comment string, unread bool) error {
	c := card{status: status, comment: comment, unread: unread}
	if c.empty() {
		return nil
	}
	err := m.do(ctx, nil, worktreeSetOp(id, c))
	var oe *orcaError
	if status != "" && errors.As(err, &oe) && oe.code != "not_found" {
		return fmt.Errorf("%w %q: %s", workspace.ErrUnknownStatus, status, oe.msg)
	}
	return err
}

func (m *Manager) MarkUnread(ctx context.Context, id string) error {
	return m.do(ctx, nil, worktreeSetOp(id, card{unread: true}))
}

// LinkUnit sets a pull request as the worktree's linkedPR; another unit's
// label goes on a line of the worktree comment, beside other units' labels.
// LinkUnit shows a pull request on the worktree's card; Orca shows no other
// unit, whose link the loop's store keeps alone.
func (m *Manager) LinkUnit(ctx context.Context, id string, u unit.Unit) error {
	if u.Provider != unit.GitHub {
		return nil
	}
	number := workspace.LinkedPR(u)
	if number <= 0 {
		return fmt.Errorf("orca: link pull request %q to %s: not a pull request key", u.Key, id)
	}
	return m.do(ctx, nil, worktreeSetOp(id, card{linkedPR: number}))
}

func (m *Manager) Remove(ctx context.Context, id string, force bool) error {
	args := []string{"worktree", "rm", "--worktree", "id:" + id}
	if force {
		args = append(args, "--force")
	}
	return m.do(ctx, nil, cliOp(args...))
}

func (m *Manager) Unregister(ctx context.Context, id string) error {
	return m.do(ctx, nil, cliOp("worktree", "rm", "--worktree", "id:"+id))
}

func (m *Manager) Rename(ctx context.Context, id, displayName string) error {
	return m.do(ctx, nil, worktreeSetOp(id, card{displayName: displayName}))
}

func (m *Manager) AgentActivity(ctx context.Context) (map[string]wsdomain.Activity, error) {
	panes, err := m.Panes(ctx)
	if err != nil {
		return nil, err
	}
	return wsdomain.ActivityOf(panes), nil
}

// Panes lists every agent of every worktree from one worktree.ps, plus a bare pane for
// the worktree open in the app when no agent runs there.
func (m *Manager) Panes(ctx context.Context) ([]wsdomain.Pane, error) {
	var res struct {
		Worktrees []struct {
			WorktreeID string `json:"worktreeId"`
			IsActive   bool   `json:"isActive"`
			Agents     []struct {
				PaneKey              string `json:"paneKey"`
				State                string `json:"state"`
				StateStartedAt       int64  `json:"stateStartedAt"`
				LastAssistantMessage string `json:"lastAssistantMessage"`
			} `json:"agents"`
		} `json:"worktrees"`
	}
	if err := m.do(ctx, &res, worktreePsOp()); err != nil {
		return nil, err
	}
	var out []wsdomain.Pane
	for _, w := range res.Worktrees {
		if len(w.Agents) == 0 && w.IsActive {
			out = append(out, wsdomain.Pane{WorkspaceID: w.WorktreeID, UserActive: true})
		}
		for _, a := range w.Agents {
			p := wsdomain.Pane{WorkspaceID: w.WorktreeID, Pane: a.PaneKey, State: turn.ParseAgentState(a.State),
				LastMessage: a.LastAssistantMessage, UserActive: w.IsActive}
			if a.StateStartedAt != 0 {
				p.StateSince = time.UnixMilli(a.StateStartedAt)
			}
			out = append(out, p)
		}
	}
	return out, nil
}

type terminalRow struct {
	Handle        string `json:"handle"`
	WorktreeID    string `json:"worktreeId"`
	TabID         string `json:"tabId"`
	Title         string `json:"title"`
	AgentIdentity string `json:"agentIdentity"`
	Connected     bool   `json:"connected"`
	Writable      bool   `json:"writable"`
	Orphaned      bool   `json:"orphaned"`
	LastOutputAt  int64  `json:"lastOutputAt"`
}

func (t terminalRow) usable() bool { return t.Connected && t.Writable && !t.Orphaned }

// terminals is the rows of the workspace's terminal list.
func (m *Manager) terminals(ctx context.Context, workspaceID string) ([]terminalRow, error) {
	var res struct {
		Terminals []terminalRow `json:"terminals"`
	}
	if err := m.do(ctx, &res, terminalListOp(workspaceID)); err != nil {
		return nil, err
	}
	return res.Terminals, nil
}

// AllTerminals lists the terminals of every worktree from one terminal.list. The scope is
// complete only when the list is whole and Orca reports its graph ready.
func (m *Manager) AllTerminals(ctx context.Context) ([]wsdomain.Terminal, wsdomain.Scope, error) {
	var res struct {
		Terminals []terminalRow `json:"terminals"`
		coverage
	}
	runtimeID, err := m.doMeta(ctx, &res, allTerminalsOp())
	if err != nil {
		return nil, wsdomain.Scope{}, err
	}
	scope := res.scope(runtimeID)
	if scope.Complete {
		var st struct {
			GraphStatus string `json:"graphStatus"`
		}
		err := m.do(ctx, &st, statusOp())
		if ctxErr := ctx.Err(); err != nil && ctxErr != nil {
			return nil, wsdomain.Scope{}, ctxErr
		}
		scope.Complete = err == nil && st.GraphStatus == "ready"
	}
	out := make([]wsdomain.Terminal, 0, len(res.Terminals))
	for _, t := range res.Terminals {
		term := wsdomain.Terminal{Handle: t.Handle, WorkspaceID: t.WorktreeID, TabID: t.TabID, Title: t.Title,
			Agent: t.AgentIdentity, Usable: t.usable()}
		if t.LastOutputAt != 0 {
			term.LastOutput = time.UnixMilli(t.LastOutputAt)
		}
		out = append(out, term)
	}
	return out, scope, nil
}

type Terminal struct {
	Handle, TabID, Title, Agent string
	LastOutput                  time.Time
}

// Terminals lists the workspace's usable terminals.
func (m *Manager) Terminals(ctx context.Context, workspaceID string) ([]Terminal, error) {
	ts, err := m.terminals(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var out []Terminal
	for _, t := range ts {
		if !t.usable() {
			continue
		}
		term := Terminal{Handle: t.Handle, TabID: t.TabID, Title: t.Title, Agent: t.AgentIdentity}
		if t.LastOutputAt != 0 {
			term.LastOutput = time.UnixMilli(t.LastOutputAt)
		}
		out = append(out, term)
	}
	return out, nil
}

type Agent struct {
	WorkspaceID, Pane, LastMessage string
	State                          turn.AgentState
	StateSince                     time.Time
	UserActive                     bool
}

func (m *Manager) Agents(ctx context.Context) ([]Agent, error) {
	panes, err := m.Panes(ctx)
	if err != nil {
		return nil, err
	}
	var out []Agent
	for _, p := range panes {
		if p.Agent() {
			out = append(out, Agent{WorkspaceID: p.WorkspaceID, Pane: p.Pane, State: p.State, StateSince: p.StateSince,
				LastMessage: p.LastMessage, UserActive: p.UserActive})
		}
	}
	return out, nil
}

// mostRecent returns the usable terminal that printed last among those that
// match, or nil.
func mostRecent(ts []terminalRow, match func(terminalRow) bool) *terminalRow {
	var best *terminalRow
	for i, t := range ts {
		if !t.usable() || !match(t) {
			continue
		}
		if best == nil || t.LastOutputAt > best.LastOutputAt {
			best = &ts[i]
		}
	}
	return best
}

// AgentIdentity is the identity Orca reports for the terminals of an agent.
func (m *Manager) AgentIdentity(kind string) string {
	if identity, ok := m.Identities[kind]; ok {
		return identity
	}
	return kind
}

// isAgent matches a terminal of the agent; an empty kind matches any agent.
func (m *Manager) isAgent(agent string) func(terminalRow) bool {
	identity := m.AgentIdentity(agent)
	return func(t terminalRow) bool {
		return t.AgentIdentity != "" && (identity == "" || t.AgentIdentity == identity)
	}
}

func (m *Manager) LiveAgentSession(ctx context.Context, workspaceID, agent string) (workspace.Session, bool, error) {
	ts, err := m.terminals(ctx, workspaceID)
	if err != nil {
		return workspace.Session{}, false, err
	}
	best := mostRecent(ts, m.isAgent(agent))
	if best == nil {
		return workspace.Session{}, false, nil
	}
	return workspace.Session{Handle: best.Handle, LastOutput: time.UnixMilli(best.LastOutputAt)}, true, nil
}

// Focus switches to the live agent terminal, else to any terminal of the
// worktree, else opens its README. Orca rejects directories and "." as paths
// for file open, so a worktree without terminals or a README cannot be focused.
func (m *Manager) Focus(ctx context.Context, id string) error {
	ts, err := m.terminals(ctx, id)
	if err != nil {
		return err
	}
	t := mostRecent(ts, m.isAgent(""))
	if t == nil {
		t = mostRecent(ts, func(terminalRow) bool { return true })
	}
	if t != nil {
		return m.do(ctx, nil, cliOp("terminal", "switch", "--terminal", t.Handle))
	}
	return m.do(ctx, nil, cliOp("file", "open", "--path", "README.md", "--worktree", "id:"+id, "--focus"))
}

func (m *Manager) StartAgentSession(ctx context.Context, req workspace.StartRequest) (string, error) {
	return m.startTerminal(ctx, req.WorkspaceID, req.Command, req.Title)
}

// Wake resumes a dormant session in a new terminal of its workspace with the
// resume line the request carries.
func (m *Manager) Wake(ctx context.Context, req workspace.WakeRequest) (string, error) {
	if req.Command == "" {
		return "", fmt.Errorf("waking the session %s needs a resume command", req.Session)
	}
	return m.startTerminal(ctx, req.WorkspaceID, req.Command, req.Title)
}

func (m *Manager) startTerminal(ctx context.Context, workspaceID, command, title string) (string, error) {
	var res struct {
		Terminal struct {
			Handle string `json:"handle"`
		} `json:"terminal"`
	}
	if err := m.do(ctx, &res, cliOp("terminal", "create", "--worktree", "id:"+workspaceID,
		"--title", title, "--command", command)); err != nil {
		return "", err
	}
	var wait struct {
		Wait struct {
			BlockedReason string `json:"blockedReason"`
		} `json:"wait"`
	}
	if err := m.do(ctx, &wait, terminalWaitOp(res.Terminal.Handle, 60000)); err != nil {
		return "", err
	}
	if reason := wait.Wait.BlockedReason; reason != "" {
		return "", &workspace.BlockedError{Handle: res.Terminal.Handle, Reason: reason}
	}
	return res.Terminal.Handle, nil
}

// WaitIdle waits up to timeout for the agent's TUI in handle to take input.
func (m *Manager) WaitIdle(ctx context.Context, handle string, timeout time.Duration) (bool, error) {
	var wait struct {
		Wait struct {
			Satisfied bool `json:"satisfied"`
		} `json:"wait"`
	}
	if err := m.do(ctx, &wait, terminalWaitOp(handle, timeout.Milliseconds())); err != nil {
		return false, err
	}
	return wait.Wait.Satisfied, nil
}

// CloseTerminal closes the terminal; one already gone, which Orca reports as
// a stale handle, is closed.
func (m *Manager) CloseTerminal(ctx context.Context, handle string) error {
	err := m.do(ctx, nil, cliOp("terminal", "close", "--terminal", handle))
	var oe *orcaError
	if errors.As(err, &oe) && oe.code == "terminal_handle_stale" {
		return nil
	}
	return err
}

// TrustsAtLaunch matches Orca's profile homes of kind, where Orca trusts the
// workspace when it launches an agent itself and its agent workspace trust
// setting is on.
func (m *Manager) TrustsAtLaunch(kind, home string) bool {
	home = filepath.Clean(home)
	profiles := filepath.Dir(filepath.Dir(home))
	return filepath.Base(home) == "home" && filepath.Base(profiles) == m.profilesDir(kind) && filepath.Base(filepath.Dir(profiles)) == "orca"
}

// ProfileHomes lists the agent homes of Orca's profiles of kind in Orca's
// user data directory for userHome, which ORCA_USER_DATA_PATH overrides.
func (m *Manager) ProfileHomes(userHome, kind string) []string {
	data := defaultUserDataPath(runtime.GOOS, os.Getenv, userHome)
	homes, _ := filepath.Glob(filepath.Join(data, m.profilesDir(kind), "*", "home"))
	return homes
}

func (m *Manager) profilesDir(kind string) string { return m.AgentIdentity(kind) + "-profiles" }

func (m *Manager) ReadScreen(ctx context.Context, handle string) (string, error) {
	var res struct {
		Terminal struct {
			Tail   []string `json:"tail"`
			Source string   `json:"source"`
		} `json:"terminal"`
	}
	if err := m.do(ctx, &res, terminalReadOp(handle)); err != nil {
		return "", err
	}
	// A host that ignores "screen" answers with accumulated output and no source.
	if res.Terminal.Source == "" {
		return "", fmt.Errorf("orca terminal read %s: the host does not support screen reads", handle)
	}
	return strings.Join(res.Terminal.Tail, "\n"), nil
}

func (m *Manager) SendPrompt(ctx context.Context, handle, prompt string, queued bool) (bool, error) {
	var res struct {
		Send struct {
			Prompt struct {
				Stages []string `json:"stages"`
			} `json:"prompt"`
		} `json:"send"`
	}
	if err := m.do(ctx, &res, cliOp("terminal", "send", "--terminal", handle,
		"--text", prompt, "--enter", "--wait-submit", "10")); err != nil {
		return false, err
	}
	// input_accepted only means the text reached the terminal.
	if queued && slices.Contains(res.Send.Prompt.Stages, "input_accepted") {
		return true, nil
	}
	return slices.Contains(res.Send.Prompt.Stages, "turn_started"), nil
}

// createFromPR follows the app's own "new workspace from pull request" path:
// Orca resolves the base and the push target, and the local branch carries the
// PR's head branch name so the app finds the PR by branch.
func (m *Manager) createFromPR(ctx context.Context, req workspace.CreateRequest) (wsdomain.Workspace, string, error) {
	repo := "id:" + req.RepoID
	var base struct {
		Error              string          `json:"error"`
		BaseBranch         string          `json:"baseBranch"`
		CompareBaseRef     string          `json:"compareBaseRef"`
		BranchNameOverride string          `json:"branchNameOverride"`
		PushTarget         json.RawMessage `json:"pushTarget"`
	}
	if err := m.do(ctx, &base, op{method: "worktree.resolvePrBase", params: map[string]any{
		"repo":              repo,
		"prNumber":          workspace.LinkedPR(req.Unit),
		"headRefName":       req.Checkout.Branch,
		"baseRefName":       req.Checkout.Base,
		"isCrossRepository": req.Checkout.Fork,
	}}); err != nil {
		return wsdomain.Workspace{}, "", err
	}
	if base.Error != "" {
		return wsdomain.Workspace{}, "", fmt.Errorf("orca rpc worktree.resolvePrBase: %s", base.Error)
	}
	params := map[string]any{
		"repo":                 repo,
		"name":                 req.Name,
		"displayName":          req.Name,
		"displayNameKind":      "user",
		"baseBranch":           base.BaseBranch,
		"compareBaseRef":       base.CompareBaseRef,
		"branchNameOverride":   base.BranchNameOverride,
		"linkedPR":             workspace.LinkedPR(req.Unit),
		"noParent":             true,
		"cliProvenanceRequest": map[string]any{},
	}
	if len(base.PushTarget) > 0 && string(base.PushTarget) != "null" {
		params["pushTarget"] = base.PushTarget
	}
	if req.Agent != "" {
		params["startupAgent"] = req.Agent
		params["startupPrompt"] = req.Prompt
		params["launchSource"] = "cli"
	}
	var res struct {
		Worktree            worktreeRow `json:"worktree"`
		AgentTerminalHandle string      `json:"agentTerminalHandle"`
		StartupTerminal     struct {
			Handle string `json:"handle"`
		} `json:"startupTerminal"`
	}
	if err := m.do(ctx, &res, op{method: "worktree.create", params: params}); err != nil {
		return wsdomain.Workspace{}, "", err
	}
	handle := res.AgentTerminalHandle
	if handle == "" {
		handle = res.StartupTerminal.Handle
	}
	return res.Worktree.workspace(), handle, nil
}
