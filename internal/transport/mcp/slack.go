package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/slack/send"
)

type Slack interface {
	LoggedIn() bool
	Send(ctx context.Context, r send.Request) (send.Sent, error)
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
	// intent must list in its draft_effects.
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
		Name:        slackSendName,
		Description: "Post a message to Slack as the user. Use it only when the user asked for this message to be sent.",
		InputSchema: object(map[string]*schema{
			"target":    str(0, "a message permalink, #channel, channel id, @handle, email or user id"),
			"text":      str(0, "the message, in Markdown"),
			"thread":    boolean("reply in the thread; defaults to true for a permalink, false otherwise"),
			"broadcast": boolean("also send a thread reply to the conversation"),
		}, "target", "text"),
	}
}

type slackSendArgs struct {
	Target    *string `json:"target"`
	Text      *string `json:"text"`
	Thread    *bool   `json:"thread"`
	Broadcast bool    `json:"broadcast"`
}

func (s *Server) slackSend(ctx context.Context, _ string, arguments json.RawMessage) any {
	var a slackSendArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		reason := "an object with target and text"
		if err != nil {
			reason += "; " + err.Error()
		}
		return text("The arguments are not "+reason+".", true)
	}
	var problems []string
	if a.Target == nil || strings.TrimSpace(*a.Target) == "" {
		problems = append(problems, "target: required")
	}
	if a.Text == nil || strings.TrimSpace(*a.Text) == "" {
		problems = append(problems, "text: required, not empty")
	}
	if len(problems) > 0 {
		return text("Nothing was sent; fix these and call "+slackSendName+" again:\n"+strings.Join(problems, "\n"), true)
	}
	if broadcastToken.MatchString(*a.Text) {
		return text("Nothing was sent: the text notifies a whole channel or group (<!channel>, <!here>, <!everyone> or a <!subteam^...> mention). Ask the user to post it themselves.", true)
	}
	if !s.Slack.LoggedIn() {
		return text(notLoggedIn(), true)
	}
	sent, err := s.Slack.Send(ctx, send.Request{
		Target: strings.TrimSpace(*a.Target), Text: *a.Text, Thread: a.Thread, Broadcast: a.Broadcast, Source: "session",
	})
	if errors.Is(err, send.ErrNotLoggedIn) {
		return text(notLoggedIn(), true)
	}
	if err != nil {
		return text("Slack send failed: "+redactTokens(err.Error()), true)
	}
	return text(postedText(sent), false)
}

func notLoggedIn() string {
	return fmt.Sprintf("not logged in to Slack; run %q", brand.Command+" slack login")
}

func postedText(s send.Sent) string {
	var b strings.Builder
	b.WriteString("Posted to " + s.Conversation)
	if s.Parent != "" {
		fmt.Fprintf(&b, " in the thread %q", s.Parent)
	}
	if s.Permalink != "" {
		b.WriteString(": " + s.Permalink)
	} else {
		fmt.Fprintf(&b, ": no permalink (channel %s, ts %s)", s.Channel, s.TS)
	}
	b.WriteString("\n\n" + s.Text)
	return b.String()
}

var slackToken = regexp.MustCompile(`(?:xox[a-z]\.)?(?:xox[a-z]|xapp)-[A-Za-z0-9-]+`)

// broadcastToken is a Slack special mention that notifies a whole channel or group.
var broadcastToken = regexp.MustCompile(`(?i)<!(?:channel|here|everyone|subteam\^)`)

func redactTokens(s string) string { return slackToken.ReplaceAllString(s, "[redacted]") }
