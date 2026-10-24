package api

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// String is s as the since parameter and the SSE id, "<epoch>:<rev>"; ""
// for the zero Since.
func (s Since) String() string {
	if s == (Since{}) {
		return ""
	}
	return s.Epoch + ":" + strconv.FormatUint(s.Rev, 10)
}

// ParseSince reads a since parameter; "" and "0" are the zero Since.
func ParseSince(raw string) (Since, error) {
	if raw == "" || raw == "0" {
		return Since{}, nil
	}
	epoch, rev, ok := strings.Cut(raw, ":")
	n, err := strconv.ParseUint(rev, 10, 64)
	if !ok || err != nil || epoch == "" {
		return Since{}, fmt.Errorf("since %q is not <epoch>:<rev>", raw)
	}
	return Since{Epoch: epoch, Rev: n}, nil
}

// Merge folds c into the change, which then says what both said; a change
// of another epoch makes it All.
func (into *Change) Merge(c Change) {
	switch {
	case c.Epoch == "":
	case into.Epoch == c.Epoch:
		into.Rev = max(into.Rev, c.Rev)
	default:
		into.All = into.All || into.Epoch != ""
		into.Epoch, into.Rev = c.Epoch, c.Rev
	}
	into.All = into.All || c.All
	into.Health = into.Health || c.Health
	into.Notices = append(into.Notices, c.Notices...)
	if c.Counters != nil {
		into.Counters = c.Counters
	}
	if into.All {
		into.Units = nil
		return
	}
	into.Units = append(into.Units, c.Units...)
	slices.SortFunc(into.Units, CompareUnits)
	into.Units = slices.Compact(into.Units)
}

// CompareUnits orders units by provider, then key.
func CompareUnits(a, b unit.Unit) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}

// In tells whether the row belongs in view; a done row is listed only in
// all and done.
func (r Row) In(view string) bool {
	switch view {
	case ViewAll:
		return true
	case ViewDone:
		return slices.Contains(r.Views, view)
	}
	return !r.Summary.Closed && slices.Contains(r.Views, view)
}

// CompareRows orders rows as every client lists them: stalled first, then
// the oldest since, then by unit.
func CompareRows(a, b Row) int {
	if a.Summary.Stalled != b.Summary.Stalled {
		if a.Summary.Stalled {
			return -1
		}
		return 1
	}
	if c := a.Summary.Since.Compare(b.Summary.Since); c != 0 {
		return c
	}
	return CompareUnits(a.Unit, b.Unit)
}
