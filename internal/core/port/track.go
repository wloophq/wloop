package port

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// TrackRead is a pull request read for a track, not stored yet: Save stores
// it once the track is admitted, so a refused track leaves no row; nil Save
// stores nothing.
type TrackRead struct {
	// Unit is the unit of the pull request GitHub names.
	Unit  unit.Unit
	State string
	Save  func(context.Context) error
}

var (
	// ErrNotFound: GitHub has no such pull request, or will not say.
	ErrNotFound = errors.New("not found")
	// ErrGitHub: the read failed on GitHub's side or on the way there.
	ErrGitHub = errors.New("GitHub")
)
