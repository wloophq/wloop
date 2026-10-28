package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// KindChat is the event kind of a message the user wrote on a task's chat: its Data is {"text": ...}.
const KindChat = "message"

// ChatRead is the mark on a task's own unit of the newest chat message its agents read or were started with.
const ChatRead = "chat.read"

// Sessions reach a task's agent, as turns.Service does: the live session of its units, typed a notice that
// the user wrote, else a new turn that resumes the last run of the task's own unit.
type Sessions interface {
	LiveOn(ctx context.Context, units ...unit.Unit) (turn.Session, bool, error)
	TellChat(ctx context.Context, sess turn.Session, id string) (bool, error)
	LastRun(ctx context.Context, u unit.Unit) (turn.Turn, bool, error)
	Launch(ctx context.Context, l turns.Launch) error
}

// TaskLog is a task's side of its chat: the messages in the event log, the marks of how far its agents
// read them, and its brief.
type TaskLog interface {
	RecordEvents(ctx context.Context, evs ...port.Event) error
	Of(ctx context.Context, id task.ID, f timeline.Filter) ([]timeline.Event, error)
	Marks(ctx context.Context, u unit.Unit) (map[string]string, error)
	SetMarks(ctx context.Context, u unit.Unit, marks map[string]string) error
	// Brief is the task's brief record's data, false when it keeps none.
	Brief(ctx context.Context, e task.Task) (string, bool, error)
}

// TaskUnits are the tasks a unit is on.
type TaskUnits interface {
	TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error)
}

// ChatDeps are what the chat on the tasks needs; a Service with zero ChatDeps serves no chat.
type ChatDeps struct {
	Sessions Sessions
	Log      TaskLog
	Units    TaskUnits
	// Intent is the profile's chat intent, the turn a message starts when no session of its task is live.
	Intent func() string
}

// ErrNoChat refuses a chat the service is not wired for.
var ErrNoChat = errors.New("the chat is not served")

// ChatRequest is a message the user wrote on a task's chat; Intent is the intent of the turn it starts
// when no session of the task is live, the profile's chat intent when "".
type ChatRequest struct {
	Task   task.ID
	Text   string
	Intent string
}

// How a chat message reached the task's agent.
const (
	ChatTyped   = "typed"
	ChatStarted = "started"
	ChatResumed = "resumed"
)

// ChatResult is where a chat message went: Route, the session typed into, or the intent and the turn its
// new turn resumes.
type ChatResult struct {
	Seq      int64
	Route    string
	Session  string
	Intent   string
	ResumeOf int64
}

// ChatRefusal is a message the loop stored on the task but could not deliver.
type ChatRefusal struct{ Why string }

func (r ChatRefusal) Error() string { return r.Why }

// Chat keeps the message on the task's own unit, then delivers it: a live session of any of the task's
// units is typed the notice that a message waits, which the agent reads with the task's read tool; with
// none, a turn of the chat intent starts on the task's own unit with the message as its guidance,
// resuming the last run there that had a session.
func (s *Service) Chat(ctx context.Context, r ChatRequest) (ChatResult, error) {
	c := s.ChatDeps
	if c.Sessions == nil || c.Log == nil {
		return ChatResult{}, ErrNoChat
	}
	text := strings.TrimSpace(r.Text)
	if text == "" {
		return ChatResult{}, ChatRefusal{"a chat message needs its text"}
	}
	e, err := s.Tasks.Task(ctx, r.Task)
	if err != nil {
		return ChatResult{}, err
	}
	own := task.UnitOf(e.ID)
	data, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return ChatResult{}, err
	}
	if err := c.Log.RecordEvents(ctx, port.Event{Unit: own, Kind: KindChat, Actor: string(task.ActorPerson), Data: string(data), At: s.now()}); err != nil {
		return ChatResult{}, err
	}
	newest, err := c.Log.Of(ctx, e.ID, timeline.Filter{Kinds: []string{KindChat}, Limit: 1})
	if err != nil {
		return ChatResult{}, err
	}
	var out ChatResult
	if len(newest) > 0 {
		out.Seq = newest[0].Seq
	}
	sess, live, err := c.Sessions.LiveOn(ctx, append([]unit.Unit{own}, e.Units...)...)
	if err != nil {
		return out, err
	}
	if live {
		out.Route, out.Session = ChatTyped, sess.ID
		submitted, err := c.Sessions.TellChat(ctx, sess, string(e.ID))
		if err != nil {
			return out, err
		}
		if !submitted {
			return out, ChatRefusal{"the message is on the task, and its notice reached the agent's terminal unsubmitted; look at its terminal"}
		}
		return out, nil
	}
	out.Intent = r.Intent
	if out.Intent == "" && c.Intent != nil {
		out.Intent = c.Intent()
	}
	if out.Intent == "" {
		return out, ChatRefusal{"the message is on the task, and no session of it is live; the profile names no chat intent to start one"}
	}
	last, ok, err := c.Sessions.LastRun(ctx, own)
	if err != nil {
		return out, err
	}
	out.Route = ChatStarted
	if ok {
		out.Route, out.ResumeOf = ChatResumed, last.Seq
	}
	if err := c.Sessions.Launch(ctx, turns.Launch{Unit: own, Intent: turn.Intent(out.Intent), ResumeOf: out.ResumeOf, Guidance: text, Trigger: turn.ByHand}); err != nil {
		return out, err
	}
	return out, s.markChatRead(ctx, own, out.Seq)
}

func (s *Service) markChatRead(ctx context.Context, own unit.Unit, seq int64) error {
	if seq == 0 {
		return nil
	}
	marks, err := s.ChatDeps.Log.Marks(ctx, own)
	if err != nil {
		return err
	}
	v, _ := mark.New(seq)
	return s.ChatDeps.Log.SetMarks(ctx, own, map[string]string{ChatRead: mark.Merge(marks[ChatRead], v)})
}

// TaskRead is what an agent reads of the task its session works on: the task, its brief, its open gates,
// the chat messages since its agents last read, oldest first, and the answers to the session's gates.
type TaskRead struct {
	Task    task.Task
	Brief   string
	Gates   []gate.Gate
	Chat    []timeline.Event
	Answers []message.Conversation
}

// ReadTask is the task's read tool: Read's answers, with the task of the session's turns, its brief, its
// open gates and the chat messages not read yet, which it marks read. A session whose turns are on no task
// reads the answers alone.
func (s *Service) ReadTask(ctx context.Context, r ReadRequest) (TaskRead, error) {
	b, err := s.bookOf(ctx, r)
	if err != nil {
		return TaskRead{}, err
	}
	convs, err := s.Read(ctx, ReadRequest{Session: r.Session, Unit: b.Unit, Message: r.Message, Gate: r.Gate})
	if err != nil {
		return TaskRead{}, err
	}
	out := TaskRead{Answers: convs}
	if s.ChatDeps.Log == nil {
		return out, nil
	}
	e, ok, err := s.taskOfUnit(ctx, b.Unit)
	if err != nil || !ok {
		return out, err
	}
	out.Task = e
	if brief, ok, err := s.ChatDeps.Log.Brief(ctx, e); err != nil {
		return out, err
	} else if ok {
		out.Brief = brief
	}
	if out.Gates, err = s.Gates.Where(ctx, gates.Query{Task: e.ID, Statuses: []gate.Status{gate.Open}}); err != nil {
		return out, err
	}
	own := task.UnitOf(e.ID)
	marks, err := s.ChatDeps.Log.Marks(ctx, own)
	if err != nil {
		return out, err
	}
	evs, err := s.ChatDeps.Log.Of(ctx, e.ID, timeline.Filter{Kinds: []string{KindChat}})
	if err != nil {
		return out, err
	}
	for _, ev := range evs {
		if ev.Unit == own && !mark.Handled(marks[ChatRead], ev.Seq) {
			out.Chat = append(out.Chat, ev)
		}
	}
	slices.Reverse(out.Chat)
	if len(out.Chat) > 0 {
		if err := s.markChatRead(ctx, own, out.Chat[len(out.Chat)-1].Seq); err != nil {
			return out, err
		}
	}
	return out, nil
}

// taskOfUnit is the task u is: the task of a task's own unit, else the first open task u is on.
func (s *Service) taskOfUnit(ctx context.Context, u unit.Unit) (task.Task, bool, error) {
	if id, ok := task.IDOf(u); ok {
		e, err := s.Tasks.Task(ctx, id)
		if err != nil {
			return task.Task{}, false, fmt.Errorf("the task of %s: %w", u.Label(), err)
		}
		return e, true, nil
	}
	if s.ChatDeps.Units == nil {
		return task.Task{}, false, nil
	}
	all, err := s.ChatDeps.Units.TasksOf(ctx, u)
	if err != nil {
		return task.Task{}, false, err
	}
	for _, e := range all {
		if e.Category != task.CategoryDone && e.Category != task.CategoryDropped {
			return e, true, nil
		}
	}
	return task.Task{}, false, nil
}

// GateOf is m's open gate; false when it has none.
func (s *Service) GateOf(ctx context.Context, m message.Message) (gate.Gate, bool, error) {
	return s.openGate(ctx, m)
}
