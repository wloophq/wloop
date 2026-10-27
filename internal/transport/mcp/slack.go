package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/wloophq/wloop/internal/slack/send"
)

// Slack posts as the user at once, for the session; session is "" for a
// caller no session took.
type Slack interface {
	LoggedIn() bool
	Send(ctx context.Context, session string, r send.Request) (send.Sent, error)
	React(ctx context.Context, session string, r send.Reaction) (send.Reacted, error)
	Edit(ctx context.Context, session string, e send.Edit) (send.Edited, error)
}

type SlackReader interface {
	Read(ctx context.Context, target string, limit int) (string, error)
}

type SlackDrafts interface {
	Draft(ctx context.Context, handle string, a DraftArgs) (DraftResult, error)
}

// DraftArgs are slack_draft's arguments.
type DraftArgs struct {
	Text     string   `json:"text"`
	Kind     string   `json:"kind,omitempty"`
	Target   string   `json:"target,omitempty"`
	Items    []string `json:"items,omitempty"`
	Reaction string   `json:"reaction,omitempty"`
	// Effects are the effects the draft declares, which the session's
	// intent must list in its drafts.allowed_effects.
	Effects []string `json:"effects,omitempty"`
}

// DraftResult is the proposed draft: where it posts once approved, and its
// state: proposed, approved by its policy, or discarded when only recorded.
type DraftResult struct {
	ID           int64
	Unit         string
	Conversation string
	State        string
}

const slackSendName = "slack_send_message"

func slackSendTool() tool {
	return tool{
		Name: slackSendName,
		InputSchema: object(map[string]*schema{
			"target":    str(0),
			"text":      str(0),
			"thread":    boolean(),
			"broadcast": boolean(),
		}, "target", "text"),
	}
}

type slackSendArgs struct {
	Target    *string `json:"target"`
	Text      *string `json:"text"`
	Thread    *bool   `json:"thread"`
	Broadcast bool    `json:"broadcast"`
}

func (s *Server) slackSend(ctx context.Context, session string, arguments json.RawMessage) any {
	var a slackSendArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return s.badArguments(slackSendName, err)
	}
	var problems []string
	if a.Target == nil || strings.TrimSpace(*a.Target) == "" {
		problems = append(problems, s.say(slackSendName, "problem_target_required", nil))
	}
	if a.Text == nil || strings.TrimSpace(*a.Text) == "" {
		problems = append(problems, s.say(slackSendName, "problem_text_required", nil))
	}
	if len(problems) > 0 {
		return s.fixThese(slackSendName, problems)
	}
	if broadcastToken.MatchString(*a.Text) {
		return s.answer(slackSendName, "broadcast", nil, true)
	}
	if !s.Slack.LoggedIn() {
		return s.notLoggedIn(slackSendName)
	}
	sent, err := s.Slack.Send(ctx, session, send.Request{
		Target: strings.TrimSpace(*a.Target), Text: *a.Text, Thread: a.Thread, Broadcast: a.Broadcast, Source: "session",
	})
	if errors.Is(err, send.ErrNotLoggedIn) {
		return s.notLoggedIn(slackSendName)
	}
	if err != nil {
		return s.failed(slackSendName, err)
	}
	return s.answer(slackSendName, "posted", fields{"Conversation": sent.Conversation, "Parent": sent.Parent, "Permalink": sent.Permalink,
		"Channel": sent.Channel, "TS": sent.TS, "Text": sent.Text}, false)
}

var slackToken = regexp.MustCompile(`(?:xox[a-z]\.)?(?:xox[a-z]|xapp)-[A-Za-z0-9-]+`)

// broadcastToken is a Slack special mention that notifies a whole channel or group.
var broadcastToken = regexp.MustCompile(`(?i)<!(?:channel|here|everyone|subteam\^)`)

func redactTokens(s string) string { return slackToken.ReplaceAllString(s, "[redacted]") }
