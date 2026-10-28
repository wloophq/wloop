package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/send"
)

// The actions the plugin runs for the host's CLI, with no unit.
const (
	// ActionSend posts a message as the user, or says where it would post.
	ActionSend = "send"
	// ActionThread names the thread unit of a message: the parent's thread
	// when it names a reply.
	ActionThread = "thread"
	// ActionEval runs a slack eval command line.
	ActionEval = "eval"
)

// SendArgs are ActionSend's arguments; DryRun posts nothing and says where
// the message would go and what it would say.
type SendArgs struct {
	Target    string `json:"target"`
	Text      string `json:"text"`
	Thread    *bool  `json:"thread,omitempty"`
	Broadcast bool   `json:"broadcast,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`
}

// SendResult is where a message went, or would go, and its text as Slack
// shows it.
type SendResult struct {
	Conversation string `json:"conversation"`
	Parent       string `json:"parent,omitempty"`
	Text         string `json:"text"`
	Permalink    string `json:"permalink,omitempty"`
}

// ThreadArgs are ActionThread's arguments.
type ThreadArgs struct {
	Message string `json:"message"`
}

// EvalArgs are ActionEval's arguments: the command line after "slack eval".
type EvalArgs struct {
	Args []string `json:"args"`
}

// EvalResult is what an eval command wrote.
type EvalResult struct {
	Output string `json:"output"`
	Log    string `json:"log,omitempty"`
}

// Action runs one of the plugin's actions.
func (s *Plugin) Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
	p, err := s.running()
	if err != nil {
		return nil, err
	}
	switch req.Action {
	case ActionSend:
		var a SendArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return nil, err
		}
		res, err := p.send(ctx, a)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	case ActionThread:
		var a ThreadArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return nil, err
		}
		if p.slack.Tokens == nil || p.slack.Client == nil {
			return nil, send.ErrNotLoggedIn
		}
		tok, err := p.slack.Tokens.Load()
		if err != nil {
			return nil, err
		}
		u, err := FollowUnit(ctx, p.slack.Client, tok.Team, a.Message)
		if err != nil {
			return nil, err
		}
		return json.Marshal(u.Wire())
	case ActionEval:
		var a EvalArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return nil, err
		}
		var out, log bytes.Buffer
		err := p.eval(ctx, a.Args, &out, &log)
		if err != nil {
			return nil, fmt.Errorf("%w%s", err, logTail(log.String()))
		}
		return json.Marshal(EvalResult{Output: out.String(), Log: log.String()})
	}
	return nil, fmt.Errorf("%w: no action %q", src.ErrNotFound, req.Action)
}

func logTail(s string) string {
	if s == "" {
		return ""
	}
	return "\n" + s
}

func decodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return errors.New("the action has no arguments")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// send posts a as the user through the plugin's sender, or, dry, resolves
// where it goes.
func (p *plugin) send(ctx context.Context, a SendArgs) (SendResult, error) {
	if p.slack.Client == nil {
		return SendResult{}, send.ErrNotLoggedIn
	}
	s := &send.Service{Client: p.slack.Client, Log: p.log, PostAllowlist: p.cfg.PostAllowlist}
	if p.db != nil {
		s.Stored = StoredOver(p.db, p.log)
	}
	r := send.Request{Target: a.Target, Text: a.Text, Thread: a.Thread, Broadcast: a.Broadcast, Source: "cli"}
	if a.DryRun {
		res, err := s.Resolve(ctx, r)
		if err != nil {
			return SendResult{}, err
		}
		if err := s.Allowed(res); err != nil {
			return SendResult{}, err
		}
		return SendResult{Conversation: res.Conversation, Parent: res.Parent, Text: send.Mrkdwn(a.Text)}, nil
	}
	sent, err := s.Send(ctx, r)
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{Conversation: sent.Conversation, Parent: sent.Parent, Text: sent.Text, Permalink: sent.Permalink}, nil
}
