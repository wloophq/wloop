package profile

import "testing"

func TestShiftHeadings(t *testing.T) {
	for _, c := range []struct {
		in   string
		top  int
		want string
	}{
		{"## A\ntext\n### B\n```\n# not a heading\n```\n#hashtag", 4, "#### A\ntext\n##### B\n```\n# not a heading\n```\n#hashtag"},
		{"# A\n###### F", 4, "#### A\n###### F"},
		{"##### A\n###### B", 4, "#### A\n##### B"},
		{"~~~~\n## in code\n~~~\n## still code\n~~~~\n## A", 4, "~~~~\n## in code\n~~~\n## still code\n~~~~\n#### A"},
		{"no headings", 4, "no headings"},
		{"   ## indented", 5, "##### indented"},
		{"    ## code block", 5, "    ## code block"},
	} {
		if got := shiftHeadings(c.in, c.top); got != c.want {
			t.Errorf("%q to %d:\n%s\nwant\n%s", c.in, c.top, got, c.want)
		}
	}
}
