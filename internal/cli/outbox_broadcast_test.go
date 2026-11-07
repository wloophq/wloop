package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestOutboxShowSaysWhetherAThreadReplyBroadcasts(t *testing.T) {
	t.Parallel()
	for broadcast, want := range map[string]string{"on": "broadcast: on\n", "off": "broadcast: off\n", "": ""} {
		var b bytes.Buffer
		printDraft(&b, api.Draft{ID: 1, State: "held", Kind: "reply", Unit: "slack/T1/D1/seg/s1", Broadcast: broadcast}, "hi")
		if got := b.String(); want != "" && !strings.Contains(got, want) || want == "" && strings.Contains(got, "broadcast") {
			t.Errorf("broadcast %q:\n%s", broadcast, got)
		}
	}
}
