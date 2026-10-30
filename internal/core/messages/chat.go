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

// The marks on a task's own unit of its chat: ChatRead, the newest message its agents read or were started
// with; ChatTold, the newest one a notice told an agent of.
const (
	ChatRead = "chat.read"
	ChatTold = "chat.told"
)

// Sessions reach a task's agent, as turns.Service does: the live session of its units, typed a notice that
// the user wrote unless its agent is busy, else a new turn that resumes the last run of the task's own unit.
type Sessions interface {
	LiveOn(ctx context.Context, units ...unit.Unit) (turn.Session, bool, error)
	Busy(ctx context.Context, sess turn.Session) (bool, error)
	TellChat(ctx context.Context, sess turn.Session, task unit.Unit) (bool, error)
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
	ChatTyped = "typed"
	// ChatQueued waits for the busy agent's next stop, which the notice blocks.
	ChatQueued  = "queued"
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
// units is typed the notice that a message waits, which the agent reads with the task's read tool, or, while
// its agent is busy, is told at its next stop; with none, a turn of the chat intent starts on the task's own
// unit with every message its agents have not read as its guidance, oldest first, resuming the last run
// there that had a session.
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
		out.Session = sess.ID
		busy, err := c.Sessions.Busy(ctx, sess)
		if err != nil {
			return out, err
		}
		if busy {
			out.Route = ChatQueued
			return out, nil
		}
		out.Route = ChatTyped
		submitted, err := c.Sessions.TellChat(ctx, sess, own)
		if err != nil {
			return out, err
		}
		if !submitted {
			return out, ChatRefusal{"the message is on the task, and its notice reached the agent's terminal unsubmitted; look at its terminal"}
		}
		return out, s.markChat(ctx, own, ChatTold, out.Seq)
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
	unread, err := s.unreadChat(ctx, e.ID)
	if err != nil {
		return out, err
	}
	texts := make([]string, 0, len(unread))
	for _, ev := range unread {
		texts = append(texts, chatText(ev))
	}
	guidance := strings.Join(texts, "\n\n")
	if err := c.Sessions.Launch(ctx, turns.Launch{Unit: own, Intent: turn.Intent(out.Intent), ResumeOf: out.ResumeOf, Guidance: guidance, Trigger: turn.ByHand}); err != nil {
		return out, err
	}
	return out, s.markChat(ctx, own, ChatRead, out.Seq)
}

// chatText is the text of chat message ev.
func chatText(ev timeline.Event) string {
	var m struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(ev.Data), &m)
	return m.Text
}

// unreadChat are the messages on task id's chat its agents have not read, oldest first.
func (s *Service) unreadChat(ctx context.Context, id task.ID) ([]timeline.Event, error) {
	own := task.UnitOf(id)
	marks, err := s.ChatDeps.Log.Marks(ctx, own)
	if err != nil {
		return nil, err
	}
	evs, err := s.ChatDeps.Log.Of(ctx, id, timeline.Filter{Kinds: []string{KindChat}})
	if err != nil {
		return nil, err
	}
	var out []timeline.Event
	for _, ev := range evs {
		if ev.Unit == own && !mark.Handled(marks[ChatRead], ev.Seq) {
			out = append(out, ev)
		}
	}
	slices.Reverse(out)
	return out, nil
}

// markChat moves the mark name of the chat on own up to seq; reading counts as told.
func (s *Service) markChat(ctx context.Context, own unit.Unit, name string, seq int64) error {
	if seq == 0 {
		return nil
	}
	marks, err := s.ChatDeps.Log.Marks(ctx, own)
	if err != nil {
		return err
	}
	v, _ := mark.New(seq)
	set := map[string]string{name: mark.Merge(marks[name], v)}
	if name == ChatRead {
		set[ChatTold] = mark.Merge(marks[ChatTold], v)
	}
	return s.ChatDeps.Log.SetMarks(ctx, own, set)
}

// Chats are the chats no notice told an agent of, which a session's stop tells of.
func (s *Service) Chats() turns.Chats { return waitingChats{s} }

type waitingChats struct{ s *Service }

func (w waitingChats) Waiting(ctx context.Context, u unit.Unit) (unit.Unit, bool, error) {
	if w.s.ChatDeps.Log == nil {
		return unit.Unit{}, false, nil
	}
	e, ok, err := w.s.taskOfUnit(ctx, u)
	if err != nil || !ok {
		return unit.Unit{}, false, err
	}
	own := task.UnitOf(e.ID)
	newest, err := w.s.ChatDeps.Log.Of(ctx, e.ID, timeline.Filter{Kinds: []string{KindChat}, Limit: 1})
	if err != nil || len(newest) == 0 || newest[0].Unit != own {
		return unit.Unit{}, false, err
	}
	marks, err := w.s.ChatDeps.Log.Marks(ctx, own)
	if err != nil {
		return unit.Unit{}, false, err
	}
	seq := newest[0].Seq
	return own, !mark.Handled(marks[ChatRead], seq) && !mark.Handled(marks[ChatTold], seq), nil
}

func (w waitingChats) Noticed(ctx context.Context, own unit.Unit) error {
	id, ok := task.IDOf(own)
	if !ok {
		return nil
	}
	newest, err := w.s.ChatDeps.Log.Of(ctx, id, timeline.Filter{Kinds: []string{KindChat}, Limit: 1})
	if err != nil || len(newest) == 0 {
		return err
	}
	return w.s.markChat(ctx, own, ChatTold, newest[0].Seq)
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
	var e task.Task
	onTask := false
	if s.ChatDeps.Log != nil {
		if e, onTask, err = s.taskOfUnit(ctx, b.Unit); err != nil {
			return TaskRead{}, err
		}
		if onTask {
			if ok, err := s.worksOn(ctx, r.Session, e); err != nil {
				return TaskRead{}, err
			} else if !ok {
				return TaskRead{}, fmt.Errorf("%w: %s", ErrNotTheTasks, e.ID)
			}
		}
	}
	convs, err := s.Read(ctx, ReadRequest{Session: r.Session, Unit: b.Unit, Message: r.Message, Gate: r.Gate})
	if err != nil {
		return TaskRead{}, err
	}
	out := TaskRead{Answers: convs}
	if !onTask {
		return out, nil
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
	if out.Chat, err = s.unreadChat(ctx, e.ID); err != nil {
		return out, err
	}
	if len(out.Chat) > 0 {
		if err := s.markChat(ctx, task.UnitOf(e.ID), ChatRead, out.Chat[len(out.Chat)-1].Seq); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ErrNotTheTasks refuses a read of a task's chat to a session with no turn on the task or a unit it holds.
var ErrNotTheTasks = errors.New("this session has no turn on the task")

// worksOn says whether session has a turn on e's own unit or on one e holds: the sessions a chat goes to.
func (s *Service) worksOn(ctx context.Context, session string, e task.Task) (bool, error) {
	if session == "" {
		return false, nil
	}
	units := append([]unit.Unit{task.UnitOf(e.ID)}, e.Units...)
	books, err := s.Books.Books(ctx, units...)
	if err != nil {
		return false, err
	}
	for _, u := range units {
		if slices.ContainsFunc(books[u].Turns, func(t turn.Turn) bool { return t.Session == session || t.SentTo == session }) {
			return true, nil
		}
	}
	return false, nil
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
