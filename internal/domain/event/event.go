// Package event holds the natural keys of GitHub events that the collector
// and the store write, and the layout of an event's time.
package event

import "time"

// TimeLayout sorts as text, which the queries that order events by time
// need.
const TimeLayout = "2006-01-02T15:04:05.000000000Z"

// RequestKey is the key of a review request or of its removal: the reviewer
// and the request's time to the second, so a re-request is its own event.
func RequestKey(reviewer string, at time.Time) string {
	return reviewer + ":" + at.UTC().Format(time.RFC3339)
}
