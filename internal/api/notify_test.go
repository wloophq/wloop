package api

import "testing"

func TestNotificationIDRoundTrips(t *testing.T) {
	for _, c := range []struct {
		cat         Category
		task, token string
	}{
		{CategoryInbox, "e-0123456789abcdef", "k3f9"},
		{CategoryOutbox, "draft:12", "20261008T100000.000000000"},
		{CategoryHeld, "e-0123456789abcdef", "2026-10-08"},
		{CategoryDigest, "-", "2026-10-08"},
		{CategoryNote, "e-1", "x"},
		{QuestionCategory(42), "e-1", "42"},
	} {
		id := NotificationID(c.cat, c.task, c.token)
		cat, task, ok := ParseNotificationID(id)
		if !ok || cat != c.cat || task != c.task {
			t.Errorf("%q parsed as %q %q %v", id, cat, task, ok)
		}
	}
	for _, id := range []string{
		"Q12|acme|api|7|12", "ROUND|acme|api|7|20261008T100000.000000000", "HELD|acme|api|7|2026-10-08",
		"", "INBOX", "INBOX|e-1", "INBOX||t", "INBOX|e-1|", "BOGUS|e-1|t", "Q|e-1|t", "Qx|e-1|t",
	} {
		if cat, task, ok := ParseNotificationID(id); ok {
			t.Errorf("%q parsed as %q %q", id, cat, task)
		}
	}
	if q, ok := QuestionCategory(42).Record(); !ok || q != 42 {
		t.Errorf("record %d %v", q, ok)
	}
	if _, ok := CategoryInbox.Record(); ok {
		t.Error("INBOX names a record")
	}
}
