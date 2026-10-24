package expr

import "testing"

func TestErrorReadsColumnMessageAndSnippet(t *testing.T) {
	e := &Error{Col: 4, Msg: "unknown field pr.mien, did you mean pr.mine?", Snippet: "!pr.mien\n   ^"}
	if got := e.Error(); got != "4: unknown field pr.mien, did you mean pr.mine?\n!pr.mien\n   ^" {
		t.Error(got)
	}
}
