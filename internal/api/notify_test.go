package api

import "testing"

func TestNotificationIDRoundTrips(t *testing.T) {
	for _, c := range []struct {
		cat         Category
		task, token string
	}{
		{CategoryNeeds, "t-0123456789abcdef", "k3f9"},
		{CategoryNeeds, "draft:12", "20261008T100000.000000000"},
		{CategoryDigest, "-", "2026-10-08"},
		{CategoryNote, "t-1", "x"},
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
	if q, ok := Category("Q42").Record(); !ok || q != 42 {
		t.Errorf("record %d %v", q, ok)
	}
	if _, ok := CategoryNeeds.Record(); ok {
		t.Error("NEEDS names a record")
	}
}

func TestOldCategoriesStillParse(t *testing.T) {
	for _, id := range []string{"INBOX|t-1|k3f9", "OUTBOX|draft:12|20261008T100000.000000000", "HELD|t-1|2026-10-08", "Q42|t-1|42", "NEEDS|t-1|k3f9"} {
		if _, task, ok := ParseNotificationID(id); !ok || task == "" {
			t.Errorf("%q does not parse", id)
		}
	}
}
