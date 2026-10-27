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
	return tool{Name: slackReactName, InputSchema: object(map[string]*schema{"permalink": str(0), "emoji": str(0), "remove": boolean()}, "permalink", "emoji")}
}

func slackEditTool() tool {
	return tool{Name: slackEditName, InputSchema: object(map[string]*schema{"permalink": str(0), "text": str(0)}, "permalink", "text")}
}

func (s *Server) slackReact(ctx context.Context, session string, arguments json.RawMessage) any {
	var a struct {
		Permalink *string `json:"permalink"`
		Emoji     *string `json:"emoji"`
		Remove    bool    `json:"remove"`
	}
	link, emoji, refusal := s.actArgs(slackReactName, "problem_emoji_required", arguments, &a, &a.Permalink, &a.Emoji)
	if refusal != nil {
		return refusal
	}
	if !s.Slack.LoggedIn() {
		return s.notLoggedIn(slackReactName)
	}
	got, err := s.Slack.React(ctx, session, send.Reaction{Permalink: link, Name: strings.Trim(strings.TrimSpace(emoji), ":"), Remove: a.Remove})
	if errors.Is(err, send.ErrNotLoggedIn) {
		return s.notLoggedIn(slackReactName)
	}
	if err != nil {
		return s.failed(slackReactName, err)
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
	return s.answer(slackReactName, name, fields{"Emoji": got.Name, "Conversation": got.Conversation, "Permalink": link}, false)
}

func (s *Server) slackEdit(ctx context.Context, session string, arguments json.RawMessage) any {
	var a struct {
		Permalink *string `json:"permalink"`
		Text      *string `json:"text"`
	}
	link, body, refusal := s.actArgs(slackEditName, "problem_text_required", arguments, &a, &a.Permalink, &a.Text)
	if refusal != nil {
		return refusal
	}
	if broadcastToken.MatchString(body) {
		return s.answer(slackEditName, "broadcast", nil, true)
	}
	if !s.Slack.LoggedIn() {
		return s.notLoggedIn(slackEditName)
	}
	got, err := s.Slack.Edit(ctx, session, send.Edit{Permalink: link, Text: body})
	if errors.Is(err, send.ErrNotLoggedIn) {
		return s.notLoggedIn(slackEditName)
	}
	if err != nil {
		return s.failed(slackEditName, err)
	}
	return s.answer(slackEditName, "edited", fields{"Conversation": got.Conversation, "Permalink": link, "Text": got.Text, "Replaced": got.Replaced}, false)
}

// actArgs decodes the arguments of slack_react or slack_edit into a, whose
// fields link and value point to: a message permalink and the other
// argument, whose absence is the answer missing. refusal is the tool's
// answer when they are not.
func (s *Server) actArgs(tool, missing string, arguments json.RawMessage, a any, link, value **string) (string, string, map[string]any) {
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return "", "", s.badArguments(tool, err)
	}
	var problems []string
	l := ""
	if *link != nil {
		l = strings.TrimSpace(**link)
	}
	if !send.IsPermalink(l) {
		problems = append(problems, s.say(tool, "problem_permalink", nil))
	}
	if *value == nil || strings.TrimSpace(**value) == "" {
		problems = append(problems, s.say(tool, missing, nil))
	}
	if len(problems) > 0 {
		return "", "", s.fixThese(tool, problems)
	}
	return l, **value, nil
}
