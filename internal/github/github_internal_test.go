package github

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/pr"
)

func TestCheckFromRollupContext(t *testing.T) {
	cases := []struct {
		in   rawCheck
		want pr.Check
	}{
		{rawCheck{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"}, pr.Check{Name: "build"}},
		{rawCheck{Name: "build", Status: "IN_PROGRESS"}, pr.Check{Name: "build", Pending: true}},
		{rawCheck{Name: "SonarCloud", Status: "COMPLETED", Conclusion: "FAILURE"}, pr.Check{Name: "SonarCloud", Failed: true}},
		{rawCheck{Name: "old", Status: "COMPLETED", Conclusion: "CANCELLED"}, pr.Check{Name: "old"}},
		{rawCheck{Context: "ci/legacy", State: "PENDING"}, pr.Check{Name: "ci/legacy", Pending: true}},
		{rawCheck{Context: "ci/legacy", State: "ERROR"}, pr.Check{Name: "ci/legacy", Failed: true}},
	}
	for _, c := range cases {
		if got := c.in.check(); got != c.want {
			t.Errorf("%+v: got %+v, want %+v", c.in, got, c.want)
		}
	}
}
