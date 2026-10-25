package cli

import (
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/github"
)

func TestAHandledAddressRoundMarksItsConflictHead(t *testing.T) {
	t.Parallel()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 9}
	key := ref.String()

	dismissed := round.New()
	skip(dismissed, key, stepAddress, roundCandidate{ConflictHead: "h1"}, true)
	if got := dismissed.Mark(key, round.AddressedConflict); got != "h1" {
		t.Errorf("a dismissed candidate: conflict head %q", got)
	}

	skipped := &sweep{st: round.New()}
	skipped.skip(step{kind: stepAddress, pr: ref, conflictHead: "h2"})
	if got := skipped.st.Mark(key, round.AddressedConflict); got != "h2" {
		t.Errorf("a skipped step: conflict head %q", got)
	}

	st, ok := blockedStep(ref, inventory.StartBlock{Round: step{kind: stepAddress}.roundKind(), ConflictHead: "h3"})
	if !ok || st.conflictHead != "h3" {
		t.Errorf("a blocked round: %+v %v", st, ok)
	}
}
