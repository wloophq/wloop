package fakesource

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/turn"
)

func TestPayloadNamesTheThread(t *testing.T) {
	u := unit.Unit{Provider: "thread", Key: "1700000000.1"}
	p, err := Payloads{}.Payload(context.Background(), turn.Request{Unit: u})
	if err != nil {
		t.Fatal(err)
	}
	if p.Unit() != u || p.Arg() != "thread:1700000000.1" || p.Wording().This != "this thread" {
		t.Fatalf("%v %q %+v", p.Unit(), p.Arg(), p.Wording())
	}
	if v, ok := p.Fact("thread.key"); !ok || v != u.Key {
		t.Errorf("thread.key = %v %t", v, ok)
	}
	if _, ok := p.Fact("pr.author"); ok {
		t.Error("a thread owns pr.author")
	}
}
