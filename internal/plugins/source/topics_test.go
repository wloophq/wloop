package source_test

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
)

func TestTopicNamesRoundTrip(t *testing.T) {
	for _, tp := range plugin.Topics {
		host := psource.HostTopics(tp)
		if host == 0 {
			t.Errorf("plugin topic %s names no host topic", tp)
			continue
		}
		if !slices.Contains(psource.WireTopics(host), tp) {
			t.Errorf("%s -> %b -> %v loses it", tp, host, psource.WireTopics(host))
		}
		for bit := port.Topic(1); bit != 0 && bit <= host; bit <<= 1 {
			if host&bit != 0 && !slices.Contains(psource.WireTopics(bit), tp) {
				t.Errorf("host topic %b of %s does not name it back", bit, tp)
			}
		}
	}
	if got := psource.WireTopics(port.TopicUsage | port.TopicApp); len(got) != 0 {
		t.Errorf("topics no plugin reads name %v", got)
	}
}

func TestAStoredReportNotifiesNoPlugin(t *testing.T) {
	if got := psource.WireTopics(port.TopicPRs); len(got) != 0 {
		t.Errorf("a stored snapshot notifies %v, so a plugin hearing it reports again", got)
	}
}
