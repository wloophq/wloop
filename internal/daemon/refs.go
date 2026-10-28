package daemon

import (
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// refOf is the pull request of a github unit, the zero reference for any other.
func refOf(u unit.Unit) pr.Ref {
	ref, _ := pr.RefOf(u)
	return ref
}

// can says whether the enabled plugin of p serves capability c.
func (s *Server) can(p unit.ProviderID, c string) bool { return api.PluginCan(s.deps.Plugins, p, c) }

// sameUnit says whether a and b name the same unit, as their provider's
// codec compares keys.
func (s *Server) sameUnit(a, b unit.Unit) bool { return s.deps.Units.Same(a, b) }

func isPullRequest(u unit.Unit) bool {
	_, ok := pr.RefOf(u)
	return ok
}

func removeDoneResult() api.RemoveDoneResult {
	return api.RemoveDoneResult{Removed: []api.Named{}, Kept: []api.Kept{}}
}

func refIntakes(ref pr.Ref, is []intake.Intake) map[pr.Ref][]intake.Intake {
	return map[pr.Ref][]intake.Intake{ref: is}
}

type turnFactMap = map[pr.Ref]prrow.TurnFact

// unitOfAddress is the unit raw names through the codecs: <provider>:<key>
// of a provider units knows, or a web address a codec reads.
func unitOfAddress(units *unit.Registry, raw string) (unit.Unit, error) {
	if p, key, ok := strings.Cut(raw, ":"); ok && !strings.HasPrefix(key, "//") {
		if c, known := units.Codec(unit.ProviderID(p)); known {
			if err := c.Valid(key); err != nil {
				return unit.Unit{}, err
			}
			return unit.Unit{Provider: unit.ProviderID(p), Key: key}, nil
		}
	}
	if u, ok := units.ParseURL(raw); ok {
		return u, nil
	}
	return unit.Unit{}, badRequest{fmt.Sprintf("%q names no unit: give a unit's web address or <provider>:<key>", raw)}
}

// parsePRURL is the unit of the pull request a URL names.
func parsePRURL(raw string) (unit.Unit, error) {
	ref, err := pr.ParseURL(raw)
	if err != nil {
		return unit.Unit{}, err
	}
	return ref.Unit(), nil
}

// named is u with its label.
func named(u unit.Unit) api.Named { return api.Named{Unit: u, Label: u.Label()} }
