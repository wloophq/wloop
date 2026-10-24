package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/wloophq/wloop/internal/slack/send"
)

const (
	slackReadName = "slack_read"
	readLimit     = 50
	readLimitMax  = 200
)

func slackReadTool() tool {
	one := 1
	limit := &schema{Type: "integer", Minimum: &one, Description: "how many messages to show, at most 200; the first message is always kept. Default 50"}
	return tool{
		Name:        slackReadName,
		Description: "Read a Slack thread or conversation, rendered: a unit (slack:<key>) or a message permalink. Stored messages come first; a channel thread not stored is read from Slack and not followed; direct messages only when tracked.",
		InputSchema: object(map[string]*schema{
			"target": str(0, "a unit (slack:<key>) or a message permalink"),
			"limit":  limit,
		}, "target"),
	}
}

type slackReadArgs struct {
	Target *string         `json:"target"`
	Limit  json.RawMessage `json:"limit"`
}

func (s *Server) slackRead(ctx context.Context, _ string, arguments json.RawMessage) any {
	var a slackReadArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		reason := "an object with target and an optional limit"
		if err != nil {
			reason += "; " + err.Error()
		}
		return text("The arguments are not "+reason+".", true)
	}
	var problems []string
	if a.Target == nil || strings.TrimSpace(*a.Target) == "" {
		problems = append(problems, "target: required")
	}
	limit := readLimit
	if a.Limit != nil {
		var n int
		if err := json.Unmarshal(a.Limit, &n); err != nil || n < 1 || n > readLimitMax {
			problems = append(problems, "limit: an integer from 1 to 200")
		}
		limit = n
	}
	if len(problems) > 0 {
		return text("Nothing was read; fix these and call "+slackReadName+" again:\n"+strings.Join(problems, "\n"), true)
	}
	if s.Slack != nil && !s.Slack.LoggedIn() {
		return text(notLoggedIn(), true)
	}
	out, err := s.SlackRead.Read(ctx, strings.TrimSpace(*a.Target), limit)
	if errors.Is(err, send.ErrNotLoggedIn) {
		return text(notLoggedIn(), true)
	}
	if err != nil {
		return text("Slack read failed: "+redactTokens(err.Error()), true)
	}
	return text(redactTokens(out), false)
}
