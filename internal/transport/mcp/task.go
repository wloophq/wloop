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
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// Tasks post an agent's message on its task, waiting on a gate, and read the task: itself, its brief, its
// gates, its chat and the user's answers.
type Tasks interface {
	Post(ctx context.Context, p messages.PostRequest) (message.Message, error)
	GateOf(ctx context.Context, m message.Message) (gate.Gate, bool, error)
	ReadTask(ctx context.Context, r messages.ReadRequest) (messages.TaskRead, error)
}

const (
	taskPostName = "task_post"
	taskReadName = "task_read"
)

func taskPostTool() tool {
	option := object(map[string]*schema{
		"id":          str(20),
		"label":       str(record.LabelMax),
		"consequence": str(record.ConsequenceMax),
	}, "id", "label", "consequence")
	return tool{
		Name: taskPostName,
		InputSchema: object(map[string]*schema{
			"text":      str(record.TextMax),
			"options":   {Type: "array", Items: option, MinItems: 2, MaxItems: 5},
			"recommend": str(record.RecommendMax),
			"confirm":   boolean(),
		}, "text"),
	}
}

type taskPostArgs struct {
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

func (s *Server) taskPost(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a taskPostArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return s.badArguments(ctx, taskPostName, err)
	}
	if a.Text == nil || strings.TrimSpace(*a.Text) == "" {
		return s.answer(ctx, taskPostName, "text_required", nil, true)
	}
	req := messages.PostRequest{Session: handle, Text: *a.Text, Recommend: a.Recommend, Confirm: a.Confirm}
	for _, o := range a.Options {
		req.Options = append(req.Options, record.Option{ID: o.ID, Label: o.Label, Consequence: o.Consequence})
	}
	m, err := s.Tasks.Post(ctx, req)
	var problems record.Problems
	var capped messages.Capped
	switch {
	case errors.As(err, &problems):
		return s.fixThese(ctx, taskPostName, problems)
	case errors.As(err, &capped):
		return s.answer(ctx, taskPostName, "capped", fields{"Error": capped.Error()}, true)
	case errors.Is(err, messages.ErrNoStartedTurn):
		return s.answer(ctx, taskPostName, "not_in_turn", fields{"Error": err.Error()}, true)
	case err != nil:
		return s.answer(ctx, taskPostName, "post_failed", fields{"Error": err.Error()}, true)
	}
	g, _, err := s.Tasks.GateOf(ctx, m)
	if err != nil {
		return s.answer(ctx, taskPostName, "post_failed", fields{"Error": err.Error()}, true)
	}
	return s.answer(ctx, taskPostName, "posted", fields{"Gate": g.ID}, false)
}

func taskReadTool() tool {
	return tool{Name: taskReadName, InputSchema: object(map[string]*schema{"gate": count()})}
}

type taskReadArgs struct {
	Gate int64 `json:"gate"`
}

func (s *Server) taskRead(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a taskReadArgs
	if len(bytes.TrimSpace(arguments)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(arguments))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
			return s.badArguments(ctx, taskReadName, err)
		}
	}
	got, err := s.Tasks.ReadTask(ctx, messages.ReadRequest{Session: handle, Gate: a.Gate})
	switch {
	case errors.Is(err, messages.ErrNoTurn):
		return s.answer(ctx, taskReadName, "no_turn", fields{"Error": err.Error()}, true)
	case errors.Is(err, messages.ErrNoMessage):
		return s.answer(ctx, taskReadName, "no_gate", fields{"ID": a.Gate}, true)
	case err != nil:
		return s.answer(ctx, taskReadName, "read_failed", fields{"Error": err.Error()}, true)
	}
	name, f := s.readTask(ctx, got, a.Gate)
	return s.answer(ctx, taskReadName, name, f, false)
}

// ReadText is what task_read answers for convs alone, read of gate id or of every unread answer when 0; the
// agent's command prints the same.
func (s *Server) ReadText(ctx context.Context, convs []message.Conversation, id int64) string {
	ctx = s.withTexts(ctx)
	name, f := s.readTask(ctx, messages.TaskRead{Answers: convs}, id)
	return s.say(ctx, taskReadName, name, f)
}

// readTask is the answer task_read gives for got: nothing when it holds nothing to read, else read with
// each of its parts worded.
func (s *Server) readTask(ctx context.Context, got messages.TaskRead, id int64) (string, fields) {
	if got.Task.ID == "" && len(got.Answers) == 0 {
		return "nothing", fields{"ID": id}
	}
	f := fields{"Task": "", "Brief": got.Brief, "Gates": "", "Chat": "", "Conversations": s.conversations(ctx, got.Answers)}
	if got.Task.ID != "" {
		f["Task"] = s.say(ctx, taskReadName, "task", fields{"ID": string(got.Task.ID), "Title": got.Task.Title, "State": got.Task.State})
	}
	gates := make([]string, len(got.Gates))
	for i, g := range got.Gates {
		gates[i] = s.say(ctx, taskReadName, "gate", fields{"ID": g.ID, "Kind": string(g.Kind), "Name": g.Name, "Intents": strings.Join(g.Intents, ", ")})
	}
	f["Gates"] = strings.Join(gates, "\n")
	chat := make([]string, len(got.Chat))
	for i, ev := range got.Chat {
		var m struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal([]byte(ev.Data), &m)
		chat[i] = s.say(ctx, taskReadName, "chat_message", fields{"Text": m.Text, "At": ev.At.UTC().Format(time.RFC3339)})
	}
	f["Chat"] = strings.Join(chat, "\n")
	return "read", f
}

// conversations is convs as task_read words them, one after another.
func (s *Server) conversations(ctx context.Context, convs []message.Conversation) string {
	parts := make([]string, 0, len(convs))
	for _, c := range convs {
		options := make([]string, len(c.Message.Options))
		for i, o := range c.Message.Options {
			options[i] = s.say(ctx, taskReadName, "option", fields{"ID": o.ID, "Label": o.Label})
		}
		answers := make([]string, len(c.Answers))
		gate := int64(0)
		for i, a := range c.Answers {
			answers[i] = s.say(ctx, taskReadName, "answer", answerFields(c.Message, a))
			gate = max(gate, a.Gate)
		}
		if c.Message.ID == 0 {
			parts = append(parts, s.say(ctx, taskReadName, "turn_answers", fields{"Answers": strings.Join(answers, "\n")}))
			continue
		}
		parts = append(parts, s.say(ctx, taskReadName, "conversation", fields{"Gate": gate, "Kind": string(c.Message.Kind), "Text": c.Message.Text,
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
