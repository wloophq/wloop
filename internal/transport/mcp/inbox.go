package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// Inbox posts an agent's message to the user's Inbox and reads the user's answers to them.
type Inbox interface {
	Post(ctx context.Context, p messages.PostRequest) (message.Message, error)
	Read(ctx context.Context, r messages.ReadRequest) ([]message.Conversation, error)
}

const inboxPostName = "inbox_post"

func inboxPostTool() tool {
	option := object(map[string]*schema{
		"id":          str(20),
		"label":       str(record.LabelMax),
		"consequence": str(record.ConsequenceMax),
	}, "id", "label", "consequence")
	return tool{
		Name: inboxPostName,
		InputSchema: object(map[string]*schema{
			"text":      str(record.TextMax),
			"options":   {Type: "array", Items: option, MinItems: 2, MaxItems: 5},
			"recommend": str(record.RecommendMax),
			"confirm":   boolean(),
		}, "text"),
	}
}

type inboxPostArgs struct {
	Text      *string      `json:"text"`
	Options   []postOption `json:"options"`
	Recommend string       `json:"recommend"`
	Confirm   bool         `json:"confirm"`
}

type postOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}

func (s *Server) inboxPost(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a inboxPostArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return s.badArguments(ctx, inboxPostName, err)
	}
	if a.Text == nil || strings.TrimSpace(*a.Text) == "" {
		return s.answer(ctx, inboxPostName, "text_required", nil, true)
	}
	req := messages.PostRequest{Session: handle, Text: *a.Text, Recommend: a.Recommend, Confirm: a.Confirm}
	for _, o := range a.Options {
		req.Options = append(req.Options, record.Option{ID: o.ID, Label: o.Label, Consequence: o.Consequence})
	}
	m, err := s.Inbox.Post(ctx, req)
	var problems record.Problems
	var capped messages.Capped
	switch {
	case errors.As(err, &problems):
		return s.fixThese(ctx, inboxPostName, problems)
	case errors.As(err, &capped):
		return s.answer(ctx, inboxPostName, "capped", fields{"Error": capped.Error()}, true)
	case errors.Is(err, messages.ErrNoStartedTurn):
		return s.answer(ctx, inboxPostName, "not_in_turn", fields{"Error": err.Error()}, true)
	case err != nil:
		return s.answer(ctx, inboxPostName, "post_failed", fields{"Error": err.Error()}, true)
	}
	return s.answer(ctx, inboxPostName, "posted", fields{"ID": int64(m.ID)}, false)
}

const inboxReadName = "inbox_read"

func inboxReadTool() tool {
	return tool{Name: inboxReadName, InputSchema: object(map[string]*schema{"message": count()})}
}

type inboxReadArgs struct {
	Message int64 `json:"message"`
}

func (s *Server) inboxRead(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a inboxReadArgs
	if len(bytes.TrimSpace(arguments)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(arguments))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
			return s.badArguments(ctx, inboxReadName, err)
		}
	}
	convs, err := s.Inbox.Read(ctx, messages.ReadRequest{Session: handle, Message: a.Message})
	switch {
	case errors.Is(err, messages.ErrNoTurn):
		return s.answer(ctx, inboxReadName, "no_turn", fields{"Error": err.Error()}, true)
	case errors.Is(err, messages.ErrNoMessage):
		return s.answer(ctx, inboxReadName, "no_message", fields{"ID": a.Message}, true)
	case err != nil:
		return s.answer(ctx, inboxReadName, "read_failed", fields{"Error": err.Error()}, true)
	}
	return s.answer(ctx, inboxReadName, readAnswer(convs), readFields(convs, s.conversations(ctx, convs), a.Message), false)
}

// ReadText is what inbox_read answers for convs, read of message id or of every unread answer when 0; the
// agent's command prints the same.
func (s *Server) ReadText(ctx context.Context, convs []message.Conversation, id int64) string {
	ctx = s.withTexts(ctx)
	return s.say(ctx, inboxReadName, readAnswer(convs), readFields(convs, s.conversations(ctx, convs), id))
}

func readAnswer(convs []message.Conversation) string {
	if len(convs) == 0 {
		return "nothing"
	}
	return "read"
}

func readFields(convs []message.Conversation, text string, id int64) fields {
	if len(convs) == 0 {
		return fields{"ID": id}
	}
	return fields{"Conversations": text}
}

// conversations is convs as inbox_read words them, one after another.
func (s *Server) conversations(ctx context.Context, convs []message.Conversation) string {
	parts := make([]string, 0, len(convs))
	for _, c := range convs {
		options := make([]string, len(c.Message.Options))
		for i, o := range c.Message.Options {
			options[i] = s.say(ctx, inboxReadName, "option", fields{"ID": o.ID, "Label": o.Label})
		}
		answers := make([]string, len(c.Answers))
		for i, a := range c.Answers {
			answers[i] = s.say(ctx, inboxReadName, "answer", answerFields(c.Message, a))
		}
		if c.Message.ID == 0 {
			parts = append(parts, s.say(ctx, inboxReadName, "turn_answers", fields{"Answers": strings.Join(answers, "\n")}))
			continue
		}
		parts = append(parts, s.say(ctx, inboxReadName, "conversation", fields{"ID": c.Message.ID, "Kind": string(c.Message.Kind), "Text": c.Message.Text,
			"Options": strings.Join(options, "\n"), "Answers": strings.Join(answers, "\n")}))
	}
	return strings.Join(parts, "\n\n")
}

func answerFields(m message.Message, a turn.Answer) fields {
	f := fields{"Option": a.OptionID, "Label": "", "Choice": "", "Text": strings.TrimSpace(a.Text), "At": a.At.UTC().Format(time.RFC3339)}
	if i := slices.IndexFunc(m.Options, func(o record.Option) bool { return o.ID == a.OptionID }); i >= 0 {
		f["Label"] = m.Options[i].Label
	}
	switch {
	case a.Yes:
		f["Choice"] = "yes"
	case a.No:
		f["Choice"] = "no"
	}
	return f
}
