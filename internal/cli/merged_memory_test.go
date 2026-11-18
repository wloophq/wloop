package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/domain/event"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// seedLearned stores one done turn on u per text, an hour apart from at, whose record learned it.
func seedLearned(t *testing.T, te *testEnv, u unit.Unit, at time.Time, texts ...string) {
	t.Helper()
	ctx := context.Background()
	b := dturn.Book{Unit: u}
	for i := range texts {
		b.Turns = append(b.Turns, dturn.Turn{ID: fmt.Sprintf("r-%s-%d", u.Key, i), Intent: "act", Trigger: "by-hand", State: dturn.Done,
			SentAt: at.Add(time.Duration(i) * time.Hour)})
	}
	saved, err := te.Turns.Store.Save(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	saved.Records = nil
	for i, text := range texts {
		saved.Records = append(saved.Records, dturn.Record{TurnSeq: saved.Turns[i].Seq, At: saved.Turns[i].SentAt,
			Record: record.Record{State: record.Done, Summary: "turn " + strconv.Itoa(i), Learned: text}})
	}
	if _, err := te.Turns.Store.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
}

func servedMemory(t *testing.T) (*testEnv, *sqlite.DB) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	if te.memory == nil {
		t.Fatal("the memory is not wired into the turns")
	}
	return te, db
}

func TestTheInstructionsCarryWhatTheTurnsOfTheTaskAndItsMergedTasksLearned(t *testing.T) {
	t.Parallel()
	te, db := servedMemory(t)
	ctx := context.Background()
	survivor := task.Task{ID: "t-00000000000000f1", State: "next", Category: task.CategoryReady, Title: "Answer Ana",
		Units: []unit.Unit{headlessThread}, Origin: task.OriginDerived, Created: testNow, Updated: testNow}
	merged := task.Task{ID: "t-00000000000000f2", State: "next", Category: task.CategoryReady, Title: "Send Ana the export",
		Origin: task.OriginUser, Created: testNow, Updated: testNow}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: survivor}, {Op: task.OpCreate, Task: merged}}); err != nil {
		t.Fatal(err)
	}
	seedLearned(t, te, task.UnitOf(merged.ID), testNow.Add(-48*time.Hour), "Ana wants March first.", "The export runs nightly.")
	seedLearned(t, te, headlessThread, testNow.Add(-24*time.Hour), "Ana is out on Fridays.", "The CSV needs a BOM.", "Finance signs off.")
	if _, err := db.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, string(merged.ID)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(merged.ID), string(survivor.ID)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES ('task', ?, ?, '', 'person', ?, ?)`,
			string(merged.ID), merge.RecordKind, testNow.Format(event.TimeLayout), `{"Into":"`+string(survivor.ID)+`"}`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	_, out, err := te.renderTurn(ctx, te.cfg, turn.Request{Intent: turn.Intent(dintent.Act), Unit: headlessThread, Payload: unitPayload{headlessThread}})
	if err != nil {
		t.Fatal(err)
	}
	section := out.Text[strings.Index(out.Text, "### What earlier turns learned"):]
	for _, want := range []string{"Finance signs off.", "The CSV needs a BOM.", "Ana is out on Fridays.", "2 earlier entries: task_read lists them all."} {
		if !strings.Contains(section, want) {
			t.Errorf("the instructions lack %q:\n%s", want, section)
		}
	}
	if strings.Contains(section, "The export runs nightly.") {
		t.Errorf("an entry past the recent ones is injected:\n%s", section)
	}

	e, err := db.Tasks().Task(ctx, survivor.ID)
	if err != nil {
		t.Fatal(err)
	}
	all, err := te.memory.OfTask(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Learned) != 5 || all.Learned[4].Text != "Ana wants March first." || all.Learned[4].Unit != task.UnitOf(merged.ID) {
		t.Fatalf("what task_read lists %+v", all.Learned)
	}
}
