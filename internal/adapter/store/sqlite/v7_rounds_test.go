package sqlite

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/round"
)

func TestV7RoundsKeepsASkippedFirstRoundAsAMark(t *testing.T) {
	t.Parallel()
	path := v6Store(t,
		`INSERT INTO round_state (provider, unit_key, agent_home, first_round_skipped) VALUES ('github', 'acme/api#1', '/h', 1)`,
		`INSERT INTO round_state (provider, unit_key, first_round_skipped) VALUES ('github', 'acme/api#2', 0)`,
	)
	db := migrateV6(t, path)
	st, err := NewRounds(db).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mark("acme/api#1", round.FirstSkipped); got != "1" {
		t.Errorf("the skipped first round: mark %q", got)
	}
	if got := st.Mark("acme/api#2", round.FirstSkipped); got != "" || st.AgentHome("acme/api#1") != "/h" {
		t.Errorf("the other pull request: mark %q, home %q", got, st.AgentHome("acme/api#1"))
	}
	if v6FirstSkipped != round.FirstSkipped {
		t.Errorf("the migration writes %q, the planner reads %q", v6FirstSkipped, round.FirstSkipped)
	}
}
