// Package rowset keeps a client's copy of the daemon's rows, merged from
// the pages of GET /api/rows.
package rowset

import (
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Set is the rows of view all as of one epoch and revision. The zero Set
// holds nothing and asks for every row.
type Set struct {
	epoch string
	rev   uint64
	rows  map[unit.Unit]api.Row
}

// Since is where the set stands, to pass to the next pull.
func (s *Set) Since() api.Since {
	if s.rows == nil {
		return api.Since{}
	}
	return api.Since{Epoch: s.epoch, Rev: s.rev}
}

// Apply merges a page of view all. A Full page replaces the set; a page
// older than the set is ignored; a page that is not Full and comes from
// another epoch, or reaches a set that holds nothing, is refused, and the
// caller pulls again from the zero Since.
func (s *Set) Apply(p api.RowsPage) (ok bool) {
	if p.Full {
		s.epoch, s.rev = p.Epoch, p.Rev
		s.rows = make(map[unit.Unit]api.Row, len(p.Rows))
		for _, r := range p.Rows {
			s.rows[r.Unit] = r
		}
		return true
	}
	if s.rows == nil || p.Epoch != s.epoch {
		return false
	}
	if p.Rev < s.rev {
		return true
	}
	for _, u := range p.Removed {
		delete(s.rows, u)
	}
	for _, r := range p.Rows {
		s.rows[r.Unit] = r
	}
	s.rev = p.Rev
	return true
}

// Row is the set's row of u.
func (s *Set) Row(u unit.Unit) (api.Row, bool) {
	r, ok := s.rows[u]
	return r, ok
}

// View is the rows of view in the order the daemon lists them.
func (s *Set) View(view string) []api.Row {
	out := make([]api.Row, 0, len(s.rows))
	for _, r := range s.rows {
		if r.In(view) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, api.CompareRows)
	return out
}
