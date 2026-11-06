package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/turn"
)

func TestTheSurvivorsDossierIncludesTheMergedTasks(t *testing.T) {
	t.Parallel()
	te, db := servedDossiers(t)
	ctx := context.Background()
	survivor := task.Task{ID: "t-00000000000000f1", State: "next", Category: task.CategoryReady, Title: "Answer Ana",
		Units: []unit.Unit{headlessThread}, Origin: task.OriginDerived, Created: testNow, Updated: testNow}
	merged := task.Task{ID: "t-00000000000000f2", State: "next", Category: task.CategoryReady, Title: "Send Ana the export",
		Origin: task.OriginUser, Created: testNow, Updated: testNow}
	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: survivor}, {Op: task.OpCreate, Task: merged}}); err != nil {
		t.Fatal(err)
	}
	mergedDir, err := te.dossierTurns.Open(ctx, task.UnitOf(merged.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mergedDir.Path, "log", "20261001T090000Z-session.md"), []byte("Ana wants March first.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, string(merged.ID)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(merged.ID), string(survivor.ID))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	mergedOwner := dossier.Owner{Unit: task.UnitOf(merged.ID)}
	if open, err := db.Dossiers().Open(ctx, mergedOwner); err != nil || !open {
		t.Fatalf("the merged task's dossier has work while its survivor is open: %v %v", open, err)
	}
	line, err := te.agentCommand(ctx, te.cfg, "/wt", roundOptions{trusted: true, unit: headlessThread})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "--add-dir '"+mergedDir.Path+"'") || strings.Contains(line, "Edit(/"+mergedDir.Path+"/log/**)") {
		t.Fatalf("the launch line does not grant the merged task's dossier read only: %s", line)
	}
	_, out, err := te.renderTurn(ctx, te.cfg, turn.Request{Intent: turn.Intent(dintent.Act), Unit: headlessThread, Payload: unitPayload{headlessThread}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "- `"+mergedDir.Path+"`") || !strings.Contains(out.Text, "never write") {
		t.Fatalf("the survivor's instructions do not name the merged task's dossier:\n%s", out.Text)
	}

	if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: func() task.Task {
		done := survivor
		done.State, done.Category = "done", task.CategoryDone
		return done
	}()}}); err != nil {
		t.Fatal(err)
	}
	if open, err := db.Dossiers().Open(ctx, mergedOwner); err != nil || open {
		t.Fatalf("the merged task's dossier still has work once its survivor is done: %v %v", open, err)
	}
}
