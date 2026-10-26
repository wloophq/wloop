package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/slack/send"
)

const (
	slackReactName = "slack_react"
	slackEditName  = "slack_edit"
	// The purposes the drafts of slack_react and slack_edit declare.
	slackReactPurpose = "react"
	slackEditPurpose  = "edit"
)

func slackReactTool() tool {
	return tool{Name: slackReactName, InputSchema: object(map[string]*schema{"permalink": str(0), "emoji": str(0)}, "permalink", "emoji")}
}

func slackEditTool() tool {
	return tool{Name: slackEditName, InputSchema: object(map[string]*schema{"permalink": str(0), "text": str(0)}, "permalink", "text")}
}

func (s *Server) slackReact(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a struct {
		Permalink *string `json:"permalink"`
		Emoji     *string `json:"emoji"`
	}
	link, emoji, refusal := actArgs(slackReactName, "emoji", arguments, &a, &a.Permalink, &a.Emoji)
	if refusal != nil {
		return refusal
	}
	return s.draft(ctx, handle, DraftArgs{Purpose: slackReactPurpose, Delivery: string(draft.Reaction), Target: link,
		Reaction: strings.Trim(strings.TrimSpace(emoji), ":")})
}

func (s *Server) slackEdit(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a struct {
		Permalink *string `json:"permalink"`
		Text      *string `json:"text"`
	}
	link, body, refusal := actArgs(slackEditName, "text", arguments, &a, &a.Permalink, &a.Text)
	if refusal != nil {
		return refusal
	}
	return s.draft(ctx, handle, DraftArgs{Purpose: slackEditPurpose, Delivery: string(draft.EditDelivery), Target: link, Text: body})
}

// actArgs decodes the arguments of slack_react or slack_edit into a, whose
// fields link and value point to: a message permalink and field. refusal is
// the tool's answer when they are not.
func actArgs(name, field string, arguments json.RawMessage, a any, link, value **string) (string, string, map[string]any) {
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		reason := "an object with permalink and " + field
		if err != nil {
			reason += " (" + err.Error() + ")"
		}
		return "", "", text("Nothing was drafted: the arguments are not "+reason+".", true)
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
		return "", "", text("Nothing was drafted; fix these and call "+name+" again:\n"+strings.Join(problems, "\n"), true)
	}
	return l, **value, nil
}
