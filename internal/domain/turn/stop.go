package turn

import "time"

type Stop struct {
	Session, PromptID, AgentSession, Message string
	At                                       time.Time
	Blocked                                  bool
	RecordID                                 int64
}

type StopFacts struct{ HasTurn, Unacked, Owes, BlockedThisPrompt bool }

type StopAction int

const (
	Allow StopAction = iota
	AckAndAllow
	LogAndAllow
	Block
	StoreUnrecorded
)

func (a StopAction) String() string {
	return [...]string{"allow", "ack-and-allow", "log-and-allow", "block", "store-unrecorded"}[a]
}

func DecideStop(f StopFacts) StopAction {
	switch {
	case !f.HasTurn:
		return Allow
	case f.Unacked:
		return AckAndAllow
	case !f.Owes:
		return LogAndAllow
	case !f.BlockedThisPrompt:
		return Block
	}
	return StoreUnrecorded
}
