package slack

import (
	"encoding/json"
	"testing"
)

func BenchmarkFromEvent(b *testing.B) {
	payload := json.RawMessage(`{"event_id":"Ev1","event":{"type":"message","channel":"C1","channel_type":"channel","user":"U1","bot_id":"B1","bot_profile":{"app_id":"A1","name":"agent"},"text":"hi <@U2> see <https://x.test/a|a> and <https://y.test/b>","ts":"2.0","thread_ts":"1.0","parent_user_id":"U3"}}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, ok, err := FromEvent("T1", payload, ViaSocket, at); !ok || err != nil {
			b.Fatal(ok, err)
		}
	}
}
