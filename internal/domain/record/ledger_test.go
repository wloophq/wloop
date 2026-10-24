package record

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const noLedgerRecord = `{"state":"asked","summary":"Publish?","question":{"kind":"confirm","text":"Publish the draft?","publishes":true},"resume":"draft at h2","next":"check CI"}`

func TestRecordWithoutLedgerUnchanged(t *testing.T) {
	r, err := Parse([]byte(noLedgerRecord))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateShape(); err != nil {
		t.Fatal(err)
	}
	if r.Ledger != nil {
		t.Fatalf("ledger %v", r.Ledger)
	}
	if stored, _ := json.Marshal(r); string(stored) != noLedgerRecord {
		t.Fatalf("stored %s", stored)
	}
	if h := r.Hash(); h != "29a8fabee1776c64d5b7d4662299096dd43a82ab1a836669cdd0d9c435482383" {
		t.Fatalf("hash %s", h)
	}
}

const twoOps = `{"state":"done","summary":"answered","ledger":[
 {"op":"create","anchor":{"ref":"C1/1700000000.000100"},"fields":{"kind":"question","summary":"Which cap?"}},
 {"op":"resolve","item":"i2","anchor":{"ref":"C1/1700000000.000200"},"resolution":"answered"}]}`

func TestParseLedgerOps(t *testing.T) {
	r, err := Parse([]byte(twoOps))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateShape(); err != nil {
		t.Fatal(err)
	}
	if len(r.Ledger) != 2 || r.Ledger[0].Op != items.OpCreate || *r.Ledger[0].Fields.Summary != "Which cap?" ||
		r.Ledger[1].Item != "i2" || r.Ledger[1].Resolution != items.Answered || r.Ledger[1].Anchor.Ref != "C1/1700000000.000200" {
		t.Fatalf("%+v", r.Ledger)
	}
}

func TestLedgerOpProblems(t *testing.T) {
	r := Record{State: Done, Summary: "s", Ledger: []items.Op{
		{Op: items.OpResolve, Item: "i1", Anchor: anchor("x"), Resolution: items.Done},
		{Op: items.OpResolve, Item: "i2", Resolution: items.Done},
		{Op: "merge", Item: "i3", Anchor: anchor("y")},
	}}
	err := r.ValidateShape()
	p, ok := err.(Problems)
	if !ok || len(p) != 2 {
		t.Fatalf("%v", err)
	}
	if !strings.HasPrefix(p[0], "ledger[1] ") || !strings.Contains(p[0], "anchor: required") {
		t.Errorf("%q", p[0])
	}
	if !strings.HasPrefix(p[1], "ledger[2] ") || !strings.Contains(p[1], `op: unknown "merge"`) {
		t.Errorf("%q", p[1])
	}
	if strings.Contains(err.Error(), "unit: required") {
		t.Errorf("a turn's op needs no unit: %v", err)
	}
}

func TestLedgerOpsBounded(t *testing.T) {
	r := Record{State: Done, Summary: "s"}
	for i := range LedgerMax + 1 {
		r.Ledger = append(r.Ledger, items.Op{Op: items.OpResolve, Item: fmt.Sprintf("i%d", i), Anchor: anchor("x"), Resolution: items.Done})
	}
	if err := r.ValidateShape(); err == nil || !strings.Contains(err.Error(), "ledger: 51 ops; at most 50") {
		t.Fatalf("%v", err)
	}
	r.Ledger = r.Ledger[:LedgerMax]
	if err := r.ValidateShape(); err != nil {
		t.Fatal(err)
	}
}

func anchor(ref string) unit.Anchor { return unit.Anchor{Ref: ref} }
