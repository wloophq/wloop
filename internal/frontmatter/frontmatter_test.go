package frontmatter

import "testing"

func TestErrorReadsFileLineColKeyMsg(t *testing.T) {
	if got := (&Error{File: "intent=review.md", Line: 2, Col: 1, Key: "delegates", Msg: "want a list"}).Error(); got != "intent=review.md:2:1: delegates: want a list" {
		t.Error(got)
	}
	if got := (&Error{File: "a.md", Line: 1, Col: 1, Msg: "front matter is TOML between +++ lines"}).Error(); got != "a.md:1:1: front matter is TOML between +++ lines" {
		t.Error(got)
	}
}

func TestSplitTakesTheFirstPlusBlock(t *testing.T) {
	head, body, line, err := Split("f.md", "+++\ntitle = \"x\"\n+++\nbody\n+++\nnot a block\n")
	if err != nil || head != "title = \"x\"" || body != "body\n+++\nnot a block\n" || line != 4 {
		t.Fatalf("%q %q %d %v", head, body, line, err)
	}
	head, body, line, err = Split("f.md", "+++\n+++\nbody")
	if err != nil || head != "" || body != "body" || line != 3 {
		t.Fatalf("%q %q %d %v", head, body, line, err)
	}
	head, body, line, err = Split("f.md", "+++\na = 1\n+++")
	if err != nil || head != "a = 1" || body != "" || line != 4 {
		t.Fatalf("%q %q %d %v", head, body, line, err)
	}
	head, body, line, err = Split("f.md", "plain\n+++\nx\n")
	if err != nil || head != "" || body != "plain\n+++\nx\n" || line != 1 {
		t.Fatalf("%q %q %d %v", head, body, line, err)
	}
}

func TestSplitRefusesALateDelimiter(t *testing.T) {
	_, _, _, err := Split("f.md", "\n+++\na = 1\n+++\nX")
	if err == nil || err.Error() != "f.md:1:1: the +++ line must be the file's first line" {
		t.Fatal(err)
	}
	_, _, _, err = Split("f.md", "+++\na = 1\n")
	if err == nil || err.Error() != "f.md:1:1: no closing +++" {
		t.Fatal(err)
	}
}

func TestSplitStripsBOMAndCRLF(t *testing.T) {
	head, body, line, err := Split("f.md", "\xef\xbb\xbf+++\r\na = 1\r\n+++\r\nP\r\n")
	if err != nil || head != "a = 1" || body != "P\n" || line != 4 {
		t.Fatalf("%q %q %d %v", head, body, line, err)
	}
}

func TestSplitReportsTheBodyLine(t *testing.T) {
	_, _, line, err := Split("f.md", "+++\na = 1\nb = 2\nc = 3\n+++\n\nX")
	if err != nil || line != 6 {
		t.Fatal(line, err)
	}
}

func TestDecodeRejectsUnknownKeysWithPosition(t *testing.T) {
	var v struct{ A, B int }
	err := Decode("f.md", []byte("a = 1\nb = 2\nreasn = 3\n"), 1, &v)
	if err == nil || err.Error() != "f.md:3:1: reasn: unknown key" {
		t.Fatal(err)
	}
	err = Decode("f.md", []byte("reasn = 3\n"), 5, &v)
	if err == nil || err.Error() != "f.md:5:1: reasn: unknown key" {
		t.Fatal(err)
	}
}

func TestDecodeSyntaxErrorHasLineAndCol(t *testing.T) {
	var v struct{ A int }
	err := Decode("f.md", []byte("A = 1\nA = = 2\n"), 2, &v)
	fe, ok := err.(*Error)
	if !ok || fe.File != "f.md" || fe.Line != 3 || fe.Col < 1 || fe.Msg == "" {
		t.Fatalf("%#v", err)
	}
}

func TestDecodeFillsTheValue(t *testing.T) {
	var v struct {
		A  int
		Ds map[string][]string
	}
	if err := Decode("f.md", []byte("A = 2\n[Ds]\nclaude = [\"/x\"]\n"), 1, &v); err != nil || v.A != 2 || v.Ds["claude"][0] != "/x" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestDashFrontMatterIsAnError(t *testing.T) {
	for _, raw := range []string{"---\ntitle: x\n---\nX", "\xef\xbb\xbf---\r\ntitle: x\r\n---\r\nX"} {
		_, _, _, err := Split("f.md", raw)
		if err == nil || err.Error() != "f.md:1:1: front matter is TOML between +++ lines" {
			t.Fatalf("%q: %v", raw, err)
		}
	}
}

func TestPositionsPlaceEveryKeyPath(t *testing.T) {
	src := "label = \"x\"\nsession = { strategy = \"new\", title = 'q' }\non_sent = [\"a\",\n  \"b\"]\n" +
		"[inputs.self_review]\npersist = true\n[[headline.new]]\nwhen = 'w'\n[[headline.new]]\ntext = \"t\"\n" +
		"[record]\nrequire = [{ state = \"done\", fields = [\"outcome.url\"] }]\n"
	got := Positions([]byte(src), 1)
	for path, want := range map[string]Position{
		"label":                       {1, 9},
		"session":                     {2, 11},
		"session.strategy":            {2, 24},
		"session.title":               {2, 39},
		"on_sent":                     {3, 1},
		"on_sent[1]":                  {4, 3},
		"inputs":                      {5, 2},
		"inputs.self_review":          {5, 2},
		"inputs.self_review.persist":  {6, 11},
		"headline.new[0].when":        {8, 8},
		"headline.new[1]":             {9, 3},
		"headline.new[1].text":        {10, 8},
		"record.require[0].state":     {12, 22},
		"record.require[0].fields[0]": {12, 40},
	} {
		if got[path] != want {
			t.Errorf("%s: got %v want %v", path, got[path], want)
		}
	}
	if p := Positions([]byte("a = 1\nb = !x\n"), 3); p["a"] != (Position{3, 5}) {
		t.Errorf("before a syntax error: %v", p)
	}
}
