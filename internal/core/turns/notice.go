package turns

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Notices words what the loop tells an agent's session, from the profile.
type Notices interface {
	// Notice is the text named name over data.
	Notice(name string, data map[string]any) (string, error)
}

// The notices the loop tells a session, and the data each reads besides Name and Command.
const (
	// NoticeAnswered tells that the user answered a gate of the session's turn: Gate, 0 for an answer that
	// names none, and Unit.
	NoticeAnswered = "answered"
	// NoticeChat tells a live session of a task that the user wrote on the task's chat: Task.
	NoticeChat = "chat"
	// NoticeRecordOwed blocks a stop whose turn has no record: Unit, Turn and ScratchDir.
	NoticeRecordOwed = "record_owed"
)

// notice is the notice name over data and the Command; with no Notices, or one that fails, its name and
// data stand in.
func (s *Service) notice(name string, data map[string]any) string {
	if s.Notices != nil {
		full := maps.Clone(data)
		full["Command"] = s.Command
		out, err := s.Notices.Notice(name, full)
		if err == nil {
			return out
		}
		s.logf("the notice %s: %v", name, err)
	}
	parts := []string{name}
	for _, k := range slices.Sorted(maps.Keys(data)) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, data[k]))
	}
	return strings.Join(parts, " ")
}

func (s *Service) answeredNotice(ref unit.Unit, a turn.Answer) string {
	return s.notice(NoticeAnswered, map[string]any{"Gate": a.Gate, "Unit": unitArg(ref)})
}

func (s *Service) blockReason(ref unit.Unit, id string) string {
	return s.notice(NoticeRecordOwed, map[string]any{"Unit": unitArg(ref), "Turn": id, "ScratchDir": turn.ScratchDir})
}
