package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	link, emoji, refusal := actArgs(slackReactName, "emoji", "Nothing was changed", arguments, &a, &a.Permalink, &a.Emoji)
	if refusal != nil {
		return refusal
	}
	if !s.Slack.LoggedIn() {
		return text(notLoggedIn(), true)
	}
	got, err := s.Slack.React(ctx, session, send.Reaction{Permalink: link, Name: strings.Trim(strings.TrimSpace(emoji), ":"), Remove: a.Remove})
	if errors.Is(err, send.ErrNotLoggedIn) {
		return text(notLoggedIn(), true)
	}
	if err != nil {
		return text("Slack reaction failed: "+redactTokens(err.Error()), true)
	}
	return text(reactedText(got, a.Remove, link), false)
}

func reactedText(r send.Reacted, remove bool, link string) string {
	switch {
	case remove && r.Unchanged:
		return fmt.Sprintf("The message had no :%s: of the user; nothing changed: %s", r.Name, link)
	case remove:
		return fmt.Sprintf("Removed :%s: from the message in %s: %s", r.Name, r.Conversation, link)
	case r.Unchanged:
		return fmt.Sprintf("The message already had :%s: of the user; nothing changed: %s", r.Name, link)
	}
	return fmt.Sprintf("Reacted :%s: to the message in %s: %s", r.Name, r.Conversation, link)
}

func (s *Server) slackEdit(ctx context.Context, session string, arguments json.RawMessage) any {
	var a struct {
		Permalink *string `json:"permalink"`
		Text      *string `json:"text"`
	}
	link, body, refusal := actArgs(slackEditName, "text", "Nothing was changed", arguments, &a, &a.Permalink, &a.Text)
	if refusal != nil {
		return refusal
	}
	if broadcastToken.MatchString(body) {
		return text("Nothing was changed: the text notifies a whole channel or group (<!channel>, <!here>, <!everyone> or a <!subteam^...> mention). Ask the user to edit it themselves.", true)
	}
	if !s.Slack.LoggedIn() {
		return text(notLoggedIn(), true)
	}
	got, err := s.Slack.Edit(ctx, session, send.Edit{Permalink: link, Text: body})
	if errors.Is(err, send.ErrNotLoggedIn) {
		return text(notLoggedIn(), true)
	}
	if err != nil {
		return text("Slack edit failed: "+redactTokens(err.Error()), true)
	}
	return text(fmt.Sprintf("Edited the message in %s: %s\n\n%s\n\nIt replaced:\n\n%s", got.Conversation, link, got.Text, got.Replaced), false)
}

// actArgs decodes the arguments of slack_react or slack_edit into a, whose
// fields link and value point to: a message permalink and field. refusal is
// the tool's answer when they are not, starting with nothing.
func actArgs(name, field, nothing string, arguments json.RawMessage, a any, link, value **string) (string, string, map[string]any) {
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		reason := "an object with permalink and " + field
		if err != nil {
			reason += " (" + err.Error() + ")"
		}
		return "", "", text(nothing+": the arguments are not "+reason+".", true)
	}
	var problems []string
	l := ""
	if *link != nil {
		l = strings.TrimSpace(**link)
	}
	if !send.IsPermalink(l) {
		problems = append(problems, "permalink: required, a Slack message permalink")
	}
	if *value == nil || strings.TrimSpace(**value) == "" {
		problems = append(problems, field+": required, not empty")
	}
	if len(problems) > 0 {
		return "", "", text(nothing+"; fix these and call "+name+" again:\n"+strings.Join(problems, "\n"), true)
	}
	return l, **value, nil
}
