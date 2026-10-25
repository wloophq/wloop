package cli

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/automation"
)

// errDraftProposeNotImplemented is what draft.propose answers until its
// implementation lands.
var errDraftProposeNotImplemented = errors.New("draft.propose is not implemented")

// draftProposeStep adapts draft.propose: Check renders the text and holds per
// workflow, subject and person from the drafts; Run proposes in the Outbox.
var draftProposeStep = stepEntry{check: (*stepSet).draftProposeCheck, run: (*stepSet).draftProposeRun}

func (s *stepSet) draftProposeCheck(ctx context.Context, c stepCall, o automation.CheckOpts) (string, error) {
	return "", errDraftProposeNotImplemented
}

func (s *stepSet) draftProposeRun(ctx context.Context, c stepCall, o automation.RunOpts) (string, error) {
	return "", errDraftProposeNotImplemented
}
