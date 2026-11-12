package gate

import "testing"

func TestAGateStampsTheSessionsThatProducedOrRevisedItsSubject(t *testing.T) {
	g := Gate{Kind: KindDraft, Session: "s-proposed", Sessions: []string{"s-proposed", "s-revised"}}
	for session, want := range map[string]bool{"s-proposed": true, "s-revised": true, "s-other": false, "": false} {
		if got := g.Stamps(session); got != want {
			t.Errorf("Stamps(%q) = %v, want %v", session, got, want)
		}
	}
	if (Gate{Session: "s-asked"}).Stamps("s-asked") != true {
		t.Error("a question gate does not stamp the session whose turn asked it")
	}
}
