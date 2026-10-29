package sqlite

import (
	"slices"
	"testing"
)

func TestV7MarksEveryResolvedGateApplied(t *testing.T) {
	t.Parallel()
	path := v6GatesFixture(t)
	migrateV6(t, path)
	raw := rawDB(t, path)
	if got := strings1(t, raw, `SELECT count(*) FROM gates WHERE status != 'open'`); slices.Equal(got, []string{"0"}) {
		t.Fatal("the fixture resolved no gate")
	}
	if got := strings1(t, raw, `SELECT id FROM gates WHERE status != 'open' AND applied_at = ''`); len(got) != 0 {
		t.Fatalf("resolved gates the first tick would replay: %q", got)
	}
	if got := strings1(t, raw, `SELECT id FROM gates WHERE status = 'open' AND applied_at != ''`); len(got) != 0 {
		t.Fatalf("open gates marked applied: %q", got)
	}
	if got := strings1(t, raw, `SELECT count(*) FROM gates WHERE status = 'open'`); slices.Equal(got, []string{"0"}) {
		t.Fatal("the fixture keeps no gate open")
	}
}
