package daemon

import (
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// refOf is the pull request of a github unit, the zero reference for any other.
func refOf(u unit.Unit) pr.Ref {
	ref, _ := port.PROf(port.Event{Unit: u})
	return ref
}

// sameUnit says whether a and b name the same unit; GitHub folds the case of
// owners and repositories.
func sameUnit(a, b unit.Unit) bool {
	if a.Provider == unit.GitHub && b.Provider == unit.GitHub {
		x, y := refOf(a), refOf(b)
		return x.Number == y.Number && strings.EqualFold(x.Owner, y.Owner) && strings.EqualFold(x.Repo, y.Repo)
	}
	return a == b
}

func isPullRequest(u unit.Unit) bool {
	_, ok := port.PROf(port.Event{Unit: u})
	return ok
}

func removeDoneResult() api.RemoveDoneResult {
	return api.RemoveDoneResult{Removed: []pr.Ref{}, Kept: []api.Kept{}}
}

func refIntakes(ref pr.Ref, is []intake.Intake) map[pr.Ref][]intake.Intake {
	return map[pr.Ref][]intake.Intake{ref: is}
}

type turnFactMap = map[pr.Ref]prrow.TurnFact

// parsePRURL is the unit of the pull request a URL names.
func parsePRURL(raw string) (unit.Unit, error) {
	ref, err := pr.ParseURL(raw)
	if err != nil {
		return unit.Unit{}, err
	}
	return ref.Unit(), nil
}
