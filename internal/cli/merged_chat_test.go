package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestTheSurvivorsChatReachesTheMergedTasksLiveSession(t *testing.T) {
	te := attachEnv(t)
	ctx := context.Background()
	inSession(te, "term_1")
	if code := te.run("task", "attach", "task:"+string(attachTask)); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	const survivor = task.ID("t-00000000000000b2")
	if err := te.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: survivor, State: "next",
		Category: task.CategoryReady, Title: "Ship the webhook sender", Origin: task.OriginUser, Created: testNow, Updated: testNow}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := te.db.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, string(attachTask)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(attachTask), string(survivor))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	id := te.sessionOf(t, "orca", "term_1")
	sessions := te.Turns.Sessions.(*turnstest.Sessions)
	sent := len(sessions.Typed())
	res, err := client.New(te.Socket).Chat(ctx, string(survivor), api.ChatRequest{Text: "Take the timeout case first."})
	if err != nil || len(res.Lines) != 1 || !strings.Contains(res.Lines[0], "live session") {
		t.Fatalf("chat on the survivor %+v %v", res, err)
	}
	if typed := sessions.Typed()[sent:]; len(typed) != 1 || typed[0].Handle != id {
		t.Fatalf("typed %+v, want the merged task's session", typed)
	}
	got, err := te.messages.ReadTask(ctx, messages.ReadRequest{Session: id})
	if err != nil || got.Task.ID != survivor || len(got.Chat) != 1 || !strings.Contains(got.Chat[0].Data, "Take the timeout case first.") {
		t.Fatalf("the merged task's session read %+v %v", got, err)
	}
}
