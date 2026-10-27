package turn

import "time"

type Stop struct {
	Session, PromptID, AgentSession, Message string
	At                                       time.Time
	Blocked                                  bool
	RecordID                                 int64
}

// StopFacts are what a stop is decided on. Answered is an answer queued for the session's turns, which the
// stop delivers before anything else.
type StopFacts struct{ HasTurn, Answered, Unacked, Owes, BlockedThisPrompt bool }

type StopAction int

const (
	Allow StopAction = iota
	AckAndAllow
	LogAndAllow
	Block
	StoreUnrecorded
	// DeliverAnswers blocks the stop with the notice of the answers queued for the session.
	DeliverAnswers
)

func (a StopAction) String() string {
	return [...]string{"allow", "ack-and-allow", "log-and-allow", "block", "store-unrecorded", "deliver-answers"}[a]
}

func DecideStop(f StopFacts) StopAction {
	switch {
	case !f.HasTurn:
		return Allow
	case f.Answered:
		return DeliverAnswers
	case f.Unacked:
		return AckAndAllow
	case !f.Owes:
		return LogAndAllow
	case !f.BlockedThisPrompt:
		return Block
	}
	return StoreUnrecorded
}
