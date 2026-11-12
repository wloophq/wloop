package draft

import "github.com/wloophq/wloop/internal/domain/unit"

// ByAgent says whether an agent proposed a draft of o: a turn, or a session through its tools.
func (o Origin) ByAgent() bool { return o.Kind == OriginTurn || o.Kind == OriginSession }

// Concerns says whether d is on u, or was proposed by a turn on u.
func (d Draft) Concerns(u unit.Unit) bool {
	return !u.IsZero() && (d.Unit == u || d.Origin.Unit == u)
}

// ProposerOf is who wrote the content a proposal of o brings: its session's, an agent's, when one proposed it
// or a turn did; a person's for a draft the user wrote with no session; a rule's otherwise.
func ProposerOf(o Origin) Actor {
	switch {
	case o.Session != "" || o.ByAgent():
		return Actor{Kind: ActorSession, Session: o.Session}
	case o.Kind == OriginUser:
		return Actor{Kind: ActorPerson}
	}
	return Actor{Kind: ActorRule}
}
