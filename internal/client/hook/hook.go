// Package hook runs the agent's SessionStart, Stop and prompt hooks and its
// MCP headers helper. A hook never
// blocks by failing: any error, panic or answer later than Budget allows,
// with the cause on standard error.
package hook

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/harness"
)

// Budget is how long a hook takes at most, reading its input included.
const Budget = 2 * time.Second

const inputMax = 1 << 20

// decode reads one JSON value of the hook's input, giving up when ctx ends:
// an input that never closes must not hold the agent.
func decode(ctx context.Context, in io.Reader) (json.RawMessage, error) {
	type result struct {
		raw json.RawMessage
		err error
	}
	done := make(chan result, 1)
	go func() {
		var raw json.RawMessage
		err := json.NewDecoder(io.LimitReader(in, inputMax)).Decode(&raw)
		done <- result{raw, err}
	}()
	select {
	case r := <-done:
		return r.raw, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("reading the hook's input: %w", ctx.Err())
	}
}

// Agent is the part of AgentAPI the hooks call.
type Agent interface {
	SessionStart(ctx context.Context, handle string, r api.SessionStartRequest) error
	Stop(ctx context.Context, handle string, r api.StopRequest) (api.StopAnswer, error)
	Prompt(ctx context.Context, handle string, r api.PromptRequest) error
}

// session is the caller's handle and kind's protocol; a "" handle means the
// session runs outside any workspace manager.
func session(env func(string) string, h harness.Set, kind string) (harness.SessionEnv, string, harness.Protocol, error) {
	p, err := h.For(kind)
	if err != nil {
		return nil, "", nil, err
	}
	s := h.Session(env)
	if s == nil {
		return nil, "", p, nil
	}
	return s, s.Handle(env), p, nil
}

func transcript(p harness.Protocol, raw []byte) string {
	if t, ok := p.(harness.Transcripts); ok {
		return t.Transcript(raw)
	}
	return ""
}

// SessionStart registers the session with the daemon. It prints nothing on
// standard output, which would enter the agent's context.
func SessionStart(ctx context.Context, env func(string) string, in io.Reader, errw io.Writer, c Agent, h harness.Set, kind string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(errw, brand.Command+" hook session-start: going on:", r)
			code = 0
		}
	}()
	s, handle, p, err := session(env, h, kind)
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" hook session-start: going on:", err)
		return 0
	}
	if handle == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	raw, err := decode(ctx, in)
	req := api.SessionStartRequest{Pane: s.Pane(env), WorktreeID: s.WorktreeID(env), Home: p.Home(env), Agent: cmp.Or(kind, h.DefaultAgent)}
	if err == nil {
		req.AgentSession, req.Source, req.Cwd, err = p.SessionStart(raw)
		req.Transcript = transcript(p, raw)
	}
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" hook session-start: going on:", err)
		return 0
	}
	if err := c.SessionStart(ctx, handle, req); err != nil {
		fmt.Fprintln(errw, brand.Command+" hook session-start: going on:", err)
	}
	return 0
}

// Stop asks the daemon whether the session may stop, and blocks once per
// prompt when its turn owes a record.
func Stop(ctx context.Context, env func(string) string, in io.Reader, out, errw io.Writer, c Agent, h harness.Set, kind string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(errw, brand.Command+" hook stop: allowing:", r)
			code = 0
		}
	}()
	_, handle, p, err := session(env, h, kind)
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" hook stop: allowing:", err)
		return 0
	}
	if handle == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	raw, err := decode(ctx, in)
	req := api.StopRequest{Agent: cmp.Or(kind, h.DefaultAgent)}
	if err == nil {
		req.AgentSession, req.PromptID, req.LastMessage, err = p.Stop(raw)
		req.Transcript = transcript(p, raw)
	}
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" hook stop: allowing:", err)
		return 0
	}
	ans, err := c.Stop(ctx, handle, req)
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" hook stop: allowing:", err)
		return 0
	}
	if ans.Block {
		_, _ = out.Write(p.Block(ans.Reason))
	}
	return 0
}

// Prompt tells the daemon the session took a prompt, so a close can tell
// whether the user typed in it. It prints nothing on standard output, which
// would enter the agent's context.
func Prompt(ctx context.Context, env func(string) string, in io.Reader, errw io.Writer, c Agent, h harness.Set, kind string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(errw, brand.Command+" hook prompt: going on:", r)
			code = 0
		}
	}()
	_, handle, p, err := session(env, h, kind)
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" hook prompt: going on:", err)
		return 0
	}
	if handle == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	raw, err := decode(ctx, in)
	var req api.PromptRequest
	if pr, ok := p.(harness.Prompts); ok && err == nil {
		req.Prompt, err = pr.Prompt(raw)
	}
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" hook prompt: going on:", err)
		return 0
	}
	if err := c.Prompt(ctx, handle, req); err != nil {
		fmt.Fprintln(errw, brand.Command+" hook prompt: going on:", err)
	}
	return 0
}

// MCPHeaders writes the headers the agent sends the loop's MCP server: the
// session's handle, from whichever manager set one. Claude Desktop leaves
// variables in a registration's headers unexpanded, and runs this helper in
// the session's environment. The daemon refuses a handle no session of the
// loop took, so an unclaimed one is sent too.
func MCPHeaders(env func(string) string, out io.Writer, h harness.Set) int {
	headers := map[string]string{}
	if handle := h.Handle(env); handle != "" {
		headers[api.SessionHeader] = handle
	}
	_ = json.NewEncoder(out).Encode(headers)
	return 0
}
