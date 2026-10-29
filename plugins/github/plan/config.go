package plan

import (
	"path"
	"slices"
	"strings"
	"time"
)

// Config is what the planner reads of the configuration: GitHub's
// settings, the board columns a finished review workspace sits in, the
// sweeps' intervals and the projects that override some settings.
type Config struct {
	ReviewPrefix string
	// Owners restricts automated rounds to these owners; empty means the
	// owners of every repository the workspace managers know.
	Owners                    []string
	TrustedAuthors            []string
	FollowupsWithoutWorkspace bool
	AddressWithoutWorkspace   bool
	AddressReviews            bool
	AddressSkipReviewers      []string
	AddressChecks             bool
	IgnoreChecks              []string
	AddressConflicts          bool
	// Completed are the completed columns of the boards.
	Completed  []string
	Fast, Slow time.Duration
	Projects   []Project
}

// Project overrides some settings for the repositories its match names;
// nil keeps the top-level value.
type Project struct {
	Match                     string
	TrustedAuthors            []string
	FollowupsWithoutWorkspace *bool
	AddressWithoutWorkspace   *bool
}

// For is the settings that apply to a pull request of owner/repo: the first
// project that matches it overrides the top-level ones.
func (c Config) For(owner, repo string) Config {
	for _, p := range c.Projects {
		ok, err := path.Match(strings.ToLower(p.Match), strings.ToLower(owner+"/"+repo))
		if err != nil || !ok {
			continue
		}
		if p.TrustedAuthors != nil {
			c.TrustedAuthors = p.TrustedAuthors
		}
		if p.FollowupsWithoutWorkspace != nil {
			c.FollowupsWithoutWorkspace = *p.FollowupsWithoutWorkspace
		}
		if p.AddressWithoutWorkspace != nil {
			c.AddressWithoutWorkspace = *p.AddressWithoutWorkspace
		}
		break
	}
	return c
}

// completed is the completed column a workspace in status sits in, "" when
// status is none of them.
func (c Config) completed(status string) string {
	if status != "" && slices.Contains(c.Completed, status) {
		return status
	}
	if len(c.Completed) > 0 {
		return c.Completed[0]
	}
	return ""
}

// maxAge is the oldest detail a plan decides on: the collector re-reads a
// followed pull request's detail once it is older than the slow interval.
func (c Config) maxAge() time.Duration { return c.Slow + 2*c.Fast }

// conflictWindow is how long a conflict must last before it is a reason,
// at least five minutes: the fast job stores a conflict and triggers the
// sweep at once.
func (c Config) conflictWindow() time.Duration { return max(c.Fast, 5*time.Minute) }
