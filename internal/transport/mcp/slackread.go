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
	limit := &schema{Type: "integer", Minimum: &one}
	return tool{
		Name: slackReadName,
		InputSchema: object(map[string]*schema{
			"target": str(0),
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
		return s.badArguments(ctx, slackReadName, err)
	}
	var problems []string
	if a.Target == nil || strings.TrimSpace(*a.Target) == "" {
		problems = append(problems, s.say(ctx, slackReadName, "problem_target_required", nil))
	}
	limit := readLimit
	if a.Limit != nil {
		var n int
		if err := json.Unmarshal(a.Limit, &n); err != nil || n < 1 || n > readLimitMax {
			problems = append(problems, s.say(ctx, slackReadName, "problem_limit", fields{"Max": readLimitMax}))
		}
		limit = n
	}
	if len(problems) > 0 {
		return s.fixThese(ctx, slackReadName, problems)
	}
	if s.Slack != nil && !s.Slack.LoggedIn() {
		return s.notLoggedIn(ctx, slackReadName)
	}
	out, err := s.SlackRead.Read(ctx, strings.TrimSpace(*a.Target), limit)
	if errors.Is(err, send.ErrNotLoggedIn) {
		return s.notLoggedIn(ctx, slackReadName)
	}
	if err != nil {
		return s.failed(ctx, slackReadName, err)
	}
	return text(redactTokens(out), false)
}
