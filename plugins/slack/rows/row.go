package slackrows

import (
	"encoding/json"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

// Kind is a Slack unit's situation kind: the extraction verdict.
type Kind string

// Row is what a Slack unit shows: its situation, its open items and its
// payload, which the plugin reports as the unit's snapshot.
type Row struct {
	Unit    unit.Unit
	Link    string
	Label   string
	Title   string
	Kind    Kind
	Views   []string
	Facets  map[string][]string
	Summary Summary
	Items   []RowItem
	Payload json.RawMessage
}

// Summary is what a row waits on and why.
type Summary struct {
	WaitsOn   string
	Since     time.Time
	Closed    bool
	OpenItems int
	Reason    string
	Line      string
}

// RowItem is an open item a row shows.
type RowItem struct {
	ID, Kind, Summary, State string
	WaitingOn                []string
	Since                    time.Time
}
