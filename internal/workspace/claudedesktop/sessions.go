package claudedesktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
)

const pollEvery = 250 * time.Millisecond

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validUUID(s string) bool { return uuidRE.MatchString(s) }

// HandleOf is the handle of the Desktop session that runs the agent session uuid.
func HandleOf(uuid string) string { return hostPrefix + uuid }

// agentState maps a registry status onto the states a workspace manager shows.
func agentState(status string) turn.AgentState {
	switch status {
	case "busy":
		return turn.AgentWorking
	case "idle":
		return turn.AgentDone
	}
	return turn.ParseAgentState(status)
}

// listed is the registry's processes whose working directory is a listed worktree.
func (m *Manager) listed(ctx context.Context) ([]entry, error) {
	wss, _, err := m.Workspaces(ctx)
	if err != nil {
		return nil, err
	}
	es, err := m.registry()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(es, func(e entry) bool {
		cwd := filepath.Clean(e.Cwd)
		return !slices.ContainsFunc(wss, func(w wsdomain.Workspace) bool { return w.ID == cwd })
	}), nil
}

func (m *Manager) Panes(ctx context.Context) ([]wsdomain.Pane, error) {
	es, err := m.listed(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wsdomain.Pane, 0, len(es))
	for _, e := range es {
		out = append(out, wsdomain.Pane{WorkspaceID: filepath.Clean(e.Cwd), Pane: e.HostSessionID,
			State: agentState(e.Status), StateSince: e.statusSince()})
	}
	return out, nil
}

func (m *Manager) AgentActivity(ctx context.Context) (map[string]wsdomain.Activity, error) {
	panes, err := m.Panes(ctx)
	if err != nil {
		return nil, err
	}
	return wsdomain.ActivityOf(panes), nil
}

// AllTerminals lists one terminal per Desktop session process in a listed
// worktree; it is complete only while the app runs.
func (m *Manager) AllTerminals(ctx context.Context) ([]wsdomain.Terminal, wsdomain.Scope, error) {
	es, err := m.listed(ctx)
	if err != nil {
		return nil, wsdomain.Scope{}, err
	}
	scope := wsdomain.Scope{Hosts: []string{wsdomain.LocalHost}}
	if id, err := m.runtime(ctx); err == nil {
		scope.Complete, scope.RuntimeID = true, id
	} else if ctx.Err() != nil {
		return nil, wsdomain.Scope{}, ctx.Err()
	}
	out := make([]wsdomain.Terminal, 0, len(es))
	for _, e := range es {
		out = append(out, wsdomain.Terminal{Handle: e.HostSessionID, WorkspaceID: filepath.Clean(e.Cwd),
			TabID: e.HostSessionID, Title: e.Name, Agent: Agent, Usable: e.speaks() == nil, LastOutput: e.statusSince()})
	}
	return out, scope, nil
}

func (m *Manager) LiveAgentSession(ctx context.Context, workspaceID, agent string) (workspace.Session, bool, error) {
	if agent != "" && agent != Agent {
		return workspace.Session{}, false, nil
	}
	es, err := m.registry()
	if err != nil {
		return workspace.Session{}, false, err
	}
	cwd := filepath.Clean(workspaceID)
	e, ok := newest(es, func(e entry) bool { return filepath.Clean(e.Cwd) == cwd && e.speaks() == nil })
	if !ok {
		return workspace.Session{}, false, nil
	}
	return workspace.Session{Handle: e.HostSessionID, LastOutput: e.statusSince()}, true, nil
}

// StartAgentSession mints the agent session req.Session in the worktree with
// the claude CLI, imports it into Desktop and waits for Desktop's process.
// The settings go to the worktree's local settings first: Desktop resumes
// with the project and local setting sources, never with flags of the loop's.
func (m *Manager) StartAgentSession(ctx context.Context, req workspace.StartRequest) (string, error) {
	if req.Agent != "" && req.Agent != Agent {
		return "", fmt.Errorf("the Claude Desktop app runs no %s sessions", req.Agent)
	}
	if !validUUID(req.Session) {
		return "", fmt.Errorf("%q is not a session uuid", req.Session)
	}
	path, err := m.worktree(req.WorkspaceID)
	if err != nil {
		return "", err
	}
	handle := HandleOf(req.Session)
	if e, ok, err := m.byHost(handle); err != nil || ok {
		if err == nil {
			err = e.speaks()
		}
		return handle, err
	}
	if err := mergeSettings(path, req.Settings); err != nil {
		return "", fmt.Errorf("writing the session's settings: %w", err)
	}
	if err := m.mint(ctx, path, req.Session, "", req.Model); err != nil {
		return "", err
	}
	if _, err := m.Exec.Run(ctx, Cmd{Name: "open", Args: []string{"-g", "claude://resume?session=" + req.Session}}); err != nil {
		return "", fmt.Errorf("importing the session into the Claude Desktop app: %w", err)
	}
	return m.await(ctx, handle)
}

// mintBootstrap is the one prompt the mint runs; Desktop shows it as the first turn.
var mintBootstrap = "This session was opened by " + brand.Name + ". Reply with the single word: ready"

// mint has claude start the conversation uuid headlessly, so Desktop can
// import it; with from, uuid continues the conversation from in a fork.
func (m *Manager) mint(ctx context.Context, dir, uuid, from, model string) error {
	args := []string{"-p", mintBootstrap, "--session-id", uuid, "--setting-sources", "user",
		"--strict-mcp-config", "--max-turns", "1", "--output-format", "json"}
	if from != "" {
		args = append(args, "--resume", from, "--fork-session")
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	out, err := m.Exec.Run(ctx, Cmd{Name: "claude", Args: args, Dir: dir, Env: mintEnv(m.environ())})
	if err != nil {
		return fmt.Errorf("minting the session: %w", err)
	}
	var res struct {
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
		Result    string `json:"result"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return fmt.Errorf("minting the session: unexpected output: %.200s", out)
	}
	if res.SessionID != uuid {
		return fmt.Errorf("minting the session %s: claude ran the session %q", uuid, res.SessionID)
	}
	if res.IsError {
		return fmt.Errorf("minting the session %s: %.200s", uuid, res.Result)
	}
	return nil
}

// mintVars are the variables of a session around the loop that would make
// the mint a child of it, or run it in another home than Desktop's.
var mintVars = []string{"CLAUDE_CONFIG_DIR", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_ENTRYPOINT", HandleVar,
	"ORCA_TERMINAL_HANDLE", "ORCA_PANE_KEY", "ORCA_WORKTREE_ID"}

func mintEnv(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(mintVars, k)
	})
}

func (m *Manager) environ() []string {
	if m.Environ == nil {
		return nil
	}
	return m.Environ()
}

// Wake gives a dormant session a process again. Desktop starts no process for
// a session it already imported until you write in it, so the conversation
// goes on in a fork under a new uuid, which Desktop imports and runs; the
// loop's session takes the new process.
func (m *Manager) Wake(ctx context.Context, req workspace.WakeRequest) (string, error) {
	from := uuidOf(req.Session, req.Handle)
	if !validUUID(from) {
		return "", fmt.Errorf("%q is not a session uuid", from)
	}
	if e, ok, err := m.byHost(HandleOf(from)); err != nil || ok {
		if err == nil {
			err = e.speaks()
		}
		return HandleOf(from), err
	}
	path, err := m.worktree(req.WorkspaceID)
	if err != nil {
		return "", err
	}
	if err := mergeSettings(path, req.Settings); err != nil {
		return "", fmt.Errorf("writing the session's settings: %w", err)
	}
	next := m.newUUID()
	if err := m.mint(ctx, path, next, from, req.Model); err != nil {
		return "", fmt.Errorf("waking the session %s: %w", req.Session, err)
	}
	if _, err := m.Exec.Run(ctx, Cmd{Name: "open", Args: []string{"-g", "claude://resume?session=" + next}}); err != nil {
		return "", fmt.Errorf("importing the woken session into the Claude Desktop app: %w", err)
	}
	_, err = m.await(ctx, HandleOf(next))
	return HandleOf(next), err
}

func (m *Manager) newUUID() string {
	if m.NewUUID != nil {
		return m.NewUUID()
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// uuidOf is Desktop's uuid of a session: its newest handle's, else the
// session id, which the loop mints as the uuid of the sessions it starts.
func uuidOf(session, handle string) string {
	if u, ok := strings.CutPrefix(handle, hostPrefix); ok {
		return u
	}
	return session
}

func (m *Manager) open(ctx context.Context, background bool, handle string) error {
	args := []string{"claude://resume?session=" + uuidOf("", handle)}
	if background {
		args = append([]string{"-g"}, args...)
	}
	_, err := m.Exec.Run(ctx, Cmd{Name: "open", Args: args})
	return err
}

// await waits up to ReadyWait for a process of the Desktop session to join the registry.
func (m *Manager) await(ctx context.Context, handle string) (string, error) {
	deadline := m.now().Add(m.readyWait())
	for {
		e, ok, err := m.byHost(handle)
		if err != nil {
			return "", err
		}
		if ok {
			return handle, e.speaks()
		}
		if !m.now().Before(deadline) {
			return "", fmt.Errorf("the Claude Desktop app started no process for %s within %s", handle, m.readyWait())
		}
		if err := m.sleep(ctx, pollEvery); err != nil {
			return "", err
		}
	}
}

func (m *Manager) readyWait() time.Duration {
	if m.ReadyWait <= 0 {
		return readyWait
	}
	return m.ReadyWait
}

func (m *Manager) sleep(ctx context.Context, d time.Duration) error {
	if m.Sleep == nil {
		return sleep(ctx, d)
	}
	return m.Sleep(ctx, d)
}

// SendPrompt writes the prompt to the process as a peer message; Claude Code
// takes a message mid-turn when the turn ends, so written counts as submitted.
func (m *Manager) SendPrompt(ctx context.Context, handle, prompt string, queued bool) (bool, error) {
	e, ok, err := m.byHost(handle)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("the Desktop session %s has no live process", handle)
	}
	if err := e.speaks(); err != nil {
		return false, err
	}
	token, err := m.token(e.PID)
	if err != nil {
		return false, err
	}
	if err := deliver(ctx, e.MessagingSocketPath, token, prompt); err != nil {
		return false, fmt.Errorf("typing into the Desktop session %s: %w", handle, err)
	}
	return true, nil
}

func deliver(ctx context.Context, socket, token, text string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	enc := json.NewEncoder(conn)
	if err := enc.Encode(map[string]any{"type": "auth", "token": token}); err != nil {
		return err
	}
	return enc.Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
}

// Focus shows the worktree's live Desktop session, else its latest open stored one.
func (m *Manager) Focus(ctx context.Context, id string) error {
	path, err := m.owned(id)
	if err != nil {
		return err
	}
	es, err := m.registry()
	if err != nil {
		return err
	}
	handle := ""
	if e, ok := newest(es, func(e entry) bool { return filepath.Clean(e.Cwd) == path }); ok {
		handle = e.HostSessionID
	} else if h, ok, err := m.lastStored(path); err != nil {
		return err
	} else if ok {
		handle = h
	}
	if !validUUID(strings.TrimPrefix(handle, hostPrefix)) {
		return fmt.Errorf("the Claude Desktop app has no session in %s", path)
	}
	return m.open(ctx, false, handle)
}

// WaitIdle waits up to timeout for the process's status to read idle.
func (m *Manager) WaitIdle(ctx context.Context, handle string, timeout time.Duration) (bool, error) {
	deadline := m.now().Add(timeout)
	for {
		e, ok, err := m.byHost(handle)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("the Desktop session %s has no live process", handle)
		}
		if e.Status == "idle" {
			return true, nil
		}
		if !m.now().Before(deadline) {
			return false, nil
		}
		if err := m.sleep(ctx, pollEvery); err != nil {
			return false, err
		}
	}
}

// worktree is the listed worktree whose id is id.
func (m *Manager) worktree(id string) (string, error) {
	path, err := m.owned(id)
	if err != nil {
		return "", err
	}
	if _, ok := worktreeAt(path); !ok {
		return "", fmt.Errorf("%s is not a worktree", path)
	}
	return path, nil
}

// CloseTerminal ends the Desktop session's process, which leaves the session
// in Desktop to resume; one with no live process is closed.
func (m *Manager) CloseTerminal(ctx context.Context, handle string) error {
	e, ok, err := m.byHost(handle)
	if err != nil || !ok {
		return err
	}
	if err := m.kill(e.PID); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("ending the process of the Desktop session %s: %w", handle, err)
	}
	return nil
}

func (m *Manager) kill(pid int) error {
	if m.Kill != nil {
		return m.Kill(pid)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
