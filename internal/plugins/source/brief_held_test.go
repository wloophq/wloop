package source_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestATaskThatStartsHoldingAnItemGetsTheAsksOfItsItems(t *testing.T) {
	log := change.NewLog(change.DefaultWindow, nil)
	db, stub, _, k := briefHostOver(t, log)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	from := log.Gen()
	wg.Go(func() { k.Notify(ctx, log, from) })
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	thread := unit.Unit{Provider: "acme", Key: "t1"}
	body := json.RawMessage(`{"goal":"Two asks in one thread","risk":"low","asks":[
		{"item":"i1","ask":"Can you export the ledger?","asker":"Ana"},
		{"item":"i2","ask":"Who sends the invoice?","asker":"Bruno"}]}`)
	if err := stub.Host().PutBrief(context.Background(), src.Brief{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Version: "x1", Body: body}); err != nil {
		t.Fatal(err)
	}
	later := task.ID("t-00000000000000c3")
	holdTask(t, db, later, thread, "i2")
	eventually(t, "the new task's brief record and its projected risk", func() bool {
		_, ok := briefOf(t, db, task.UnitOf(later))
		p, _ := db.Props(nil).PropsOf(context.Background(), later)
		return ok && p["risk"].Text == "low"
	})
	b, _ := briefOf(t, db, task.UnitOf(later))
	asks, _ := b["asks"].([]any)
	if len(asks) != 1 || asks[0].(map[string]any)["ask"] != "Who sends the invoice?" {
		t.Fatalf("the task's brief %v, want the ask of its item only", b)
	}
}
