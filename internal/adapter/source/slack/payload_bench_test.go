package slack

import (
	"context"
	"fmt"
	"testing"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/turn"
)

// BenchmarkPayloadOfALongThread reads the payload of a 2000-message thread, cut to the default cap.
func BenchmarkPayloadOfALongThread(b *testing.B) {
	f := newFixture(b)
	f.login(b)
	msgs := make([]dslack.Message, 2000)
	for i := range msgs {
		msgs[i] = dslack.Message{TS: fmt.Sprintf("1700000000.%06d", 100+i), ThreadTS: readParent, User: other, Text: fmt.Sprintf("message %d with some words in it", i)}
	}
	msgs[0].TS = readParent
	f.seed(b, dslack.Facts{Threads: []dslack.Thread{{Conversation: readConv, TS: readParent, Messages: msgs}}})
	u := dslack.ThreadUnit(team, readConv, readParent)
	r := f.payloads()
	b.ResetTimer()
	for range b.N {
		if _, err := r.Payload(context.Background(), turn.Request{Unit: u}); err != nil {
			b.Fatal(err)
		}
	}
}
