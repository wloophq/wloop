package intent

import "testing"

func TestTitleIsTheFirstLineCutAtAWord(t *testing.T) {
	for _, c := range []struct {
		n       int
		in, out string
	}{
		{72, "\n## Add retries\n\nthe diagnosis", "Add retries"},
		{20, "retry the webhook delivery with backoff", "retry the webhook…"},
		{20, "retry the webhook de", "retry the webhook de"},
		{10, "averyveryverylongword", "averyveryv…"},
		{0, "# Kept whole", "Kept whole"},
	} {
		if got := title(c.n, c.in); got != c.out {
			t.Errorf("title(%d, %q) = %q, want %q", c.n, c.in, got, c.out)
		}
	}
}
