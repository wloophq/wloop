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
	slackReactName = "slack_react"
	slackEditName  = "slack_edit"
)

func slackReactTool() tool {
	return tool{Name: slackReactName, InputSchema: object(map[string]*schema{"message": str(0), "emoji": str(0), "remove": boolean()}, "message", "emoji")}
}

func slackEditTool() tool {
	return tool{Name: slackEditName, InputSchema: object(map[string]*schema{"message": str(0), "text": str(0)}, "message", "text")}
}

func (s *Server) slackReact(ctx context.Context, session string, arguments json.RawMessage) any {
	var a struct {
		Message *string `json:"message"`
		Emoji   *string `json:"emoji"`
		Remove  bool    `json:"remove"`
	}
	msg, emoji, refusal := s.actArgs(ctx, slackReactName, "problem_emoji_required", arguments, &a, &a.Message, &a.Emoji)
	if refusal != nil {
		return refusal
	}
	if !s.Slack.LoggedIn() {
		return s.notLoggedIn(ctx, slackReactName)
	}
	got, err := s.Slack.React(ctx, session, send.Reaction{Message: msg, Name: strings.Trim(strings.TrimSpace(emoji), ":"), Remove: a.Remove})
	if errors.Is(err, send.ErrNotLoggedIn) {
		return s.notLoggedIn(ctx, slackReactName)
	}
	if err != nil {
		return s.failed(ctx, slackReactName, err)
	}
	name := "reacted"
	switch {
	case a.Remove && got.Unchanged:
		name = "absent"
	case a.Remove:
		name = "removed"
	case got.Unchanged:
		name = "already"
	}
	return s.answer(ctx, slackReactName, name, fields{"Emoji": got.Name, "Conversation": got.Conversation, "Message": send.Ref(got.Channel, got.TS),
		"Permalink": got.Permalink}, false)
}

func (s *Server) slackEdit(ctx context.Context, session string, arguments json.RawMessage) any {
	var a struct {
		Message *string `json:"message"`
		Text    *string `json:"text"`
	}
	msg, body, refusal := s.actArgs(ctx, slackEditName, "problem_text_required", arguments, &a, &a.Message, &a.Text)
	if refusal != nil {
		return refusal
	}
	if broadcastToken.MatchString(body) {
		return s.answer(ctx, slackEditName, "broadcast", nil, true)
	}
	if !s.Slack.LoggedIn() {
		return s.notLoggedIn(ctx, slackEditName)
	}
	got, err := s.Slack.Edit(ctx, session, send.Edit{Message: msg, Text: body})
	if errors.Is(err, send.ErrNotLoggedIn) {
		return s.notLoggedIn(ctx, slackEditName)
	}
	if err != nil {
		return s.failed(ctx, slackEditName, err)
	}
	return s.answer(ctx, slackEditName, "edited", fields{"Conversation": got.Conversation, "Message": send.Ref(got.Channel, got.TS), "Permalink": got.Permalink,
		"Text": got.Text, "Replaced": got.Replaced}, false)
}

// actArgs decodes the arguments of slack_react or slack_edit into a, whose
// fields msg and value point to: a message ref or permalink and the other
// argument, whose absence is the answer missing. refusal is the tool's
// answer when they are not.
func (s *Server) actArgs(ctx context.Context, tool, missing string, arguments json.RawMessage, a any, msg, value **string) (string, string, map[string]any) {
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return "", "", s.badArguments(ctx, tool, err)
	}
	var problems []string
	m := ""
	if *msg != nil {
		m = strings.TrimSpace(**msg)
	}
	if !send.IsMessage(m) {
		problems = append(problems, s.say(ctx, tool, "problem_message", nil))
	}
	if *value == nil || strings.TrimSpace(**value) == "" {
		problems = append(problems, s.say(ctx, tool, missing, nil))
	}
	if len(problems) > 0 {
		return "", "", s.fixThese(ctx, tool, problems)
	}
	return m, **value, nil
}
