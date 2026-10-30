package source

import (
	"slices"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/pkg/plugin"
)

// topicNames are the host's change topics behind each plugin topic, the one
// name table between the change log and the wire.
var topicNames = []struct {
	wire plugin.Topic
	host port.Topic
}{
	{plugin.TopicRounds, port.TopicRounds | port.TopicPause | port.TopicPending},
	{plugin.TopicGates, port.TopicGates | port.TopicDrafts},
	{plugin.TopicMarks, port.TopicMarks},
	{plugin.TopicTasks, port.TopicTasks},
	{plugin.TopicTurns, port.TopicTurns},
	{plugin.TopicSessions, port.TopicSessions},
	{plugin.TopicOverrides, port.TopicOverrides},
	{plugin.TopicViewer, port.TopicViewer},
	{plugin.TopicConfig, port.TopicConfig},
	{plugin.TopicEvents, port.TopicSourceEvents | port.TopicLoopEvents},
	{plugin.TopicWorkspaces, port.TopicAgents | port.TopicFollow},
	{plugin.TopicAgents, port.TopicAgents},
}

// WireTopics are the plugin topics t, a set of host topics, touches.
func WireTopics(t port.Topic) []plugin.Topic {
	var out []plugin.Topic
	for _, n := range topicNames {
		if t&n.host != 0 && !slices.Contains(out, n.wire) {
			out = append(out, n.wire)
		}
	}
	return out
}

// HostTopics are the host topics behind ts.
func HostTopics(ts ...plugin.Topic) port.Topic {
	var out port.Topic
	for _, n := range topicNames {
		if slices.Contains(ts, n.wire) {
			out |= n.host
		}
	}
	return out
}
