package api

import (
	"encoding/json"
	"maps"
	"testing"
)

func TestAPITextMarshalsAStringOrForms(t *testing.T) {
	for _, c := range []struct {
		text Text
		want string
	}{
		{Text{"other": "a"}, `"a"`},
		{Text{"one": "a", "other": "b"}, `{"one":"a","other":"b"}`},
	} {
		b, err := json.Marshal(c.text)
		if err != nil || string(b) != c.want {
			t.Errorf("%v: %s %v", c.text, b, err)
		}
		var back Text
		if err := json.Unmarshal(b, &back); err != nil || !maps.Equal(back, c.text) {
			t.Errorf("%s: %v %v", b, back, err)
		}
	}
	var s Strings
	if err := json.Unmarshal([]byte(`{"locale":"pt-BR","strings":{"a":"A","{n} b":{"one":"{n} B","other":"{n} Bs"}},"formats":{"time":"15:04"}}`), &s); err != nil ||
		s.Strings["a"]["other"] != "A" || s.Strings["{n} b"]["one"] != "{n} B" || s.Formats["time"] != "15:04" {
		t.Fatalf("%+v %v", s, err)
	}
	if err := json.Unmarshal([]byte(`3`), new(Text)); err == nil {
		t.Fatal("a number read as a text")
	}
}
