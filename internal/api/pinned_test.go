package api

import "testing"

// TestDraftOfSkipsAPinnedDraft finds a task's draft among its draft gates past one an open question pins, which
// that question decides.
func TestDraftOfSkipsAPinnedDraft(t *testing.T) {
	e := TaskView{Gates: []GateView{DraftGate(Draft{ID: 42, PinnedBy: 3}), DraftGate(Draft{ID: 44})}}
	if d, ok := e.DraftOf(); !ok || d.ID != 44 {
		t.Fatalf("DraftOf %+v %v, want draft 44", d, ok)
	}
	if _, ok := (TaskView{Gates: []GateView{DraftGate(Draft{ID: 42, PinnedBy: 3})}}).DraftOf(); ok {
		t.Fatal("a task whose only draft a question pins has a draft of its own")
	}
}
