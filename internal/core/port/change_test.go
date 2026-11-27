package port

import (
	"slices"
	"testing"
)

func TestEveryTopicHasOneName(t *testing.T) {
	var seen []string
	for i := range len(topicNames) {
		names := Topic(1 << i).Names()
		if len(names) != 1 || names[0] == "" || slices.Contains(seen, names[0]) {
			t.Fatalf("topic bit %d names %v", i, names)
		}
		seen = append(seen, names[0])
	}
	if AllTopics != Topic(1)<<len(topicNames)-1 {
		t.Fatalf("AllTopics %b covers other topics than the %d named", AllTopics, len(topicNames))
	}
	if got := (TopicTasks | TopicApp).Names(); !slices.Equal(got, []string{"app", "tasks"}) {
		t.Fatalf("names %v", got)
	}
}
