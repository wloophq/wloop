package tui

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDecode(t *testing.T) {
	cases := map[string][]Key{
		"\x1b[A\x1b[B":            {{Name: KeyUp}, {Name: KeyDown}},
		"\x1bOA":                  {{Name: KeyUp}},
		"\x1b":                    {{Name: KeyEsc}},
		"\x1b\x1b":                {{Name: KeyEsc}, {Name: KeyEsc}},
		"\r\x7f\t\x03":            {{Name: KeyEnter}, {Name: KeyBackspace}, {Name: KeyTab}, {Name: KeyCtrlC}},
		"\x1a":                    {{Name: KeyCtrlZ}},
		"qé":                      {{Rune: 'q'}, {Rune: 'é'}},
		"\x1b[1;5C x":             {{Rune: ' '}, {Rune: 'x'}},
		"\x1bb\x1b\x7fx":          {{Rune: 'x'}},
		"\x01\xff\x1b[":           {},
		"\x1b[200~a\nb\x1b[201~q": {{Name: KeyPaste, Text: "a\nb"}, {Rune: 'q'}},
	}
	for in, want := range cases {
		if got := Decode([]byte(in)); !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%q: %+v, want %+v", in, got, want)
		}
	}
}

func TestDecoderHoldsWhatASplitReadCut(t *testing.T) {
	for _, c := range []struct {
		parts []string
		want  []Key
	}{
		{[]string{"\x1b", "[A"}, []Key{{Name: KeyUp}}},
		{[]string{"\x1b[", "A"}, []Key{{Name: KeyUp}}},
		{[]string{"\x1bO", "A"}, []Key{{Name: KeyUp}}},
		{[]string{"\x1b[1;", "5A"}, nil},
		{[]string{"\xc3", "\xa9"}, []Key{{Rune: 'é'}}},
		{[]string{"\x1b[200~pas", "ted\x1b[2", "01~"}, []Key{{Name: KeyPaste, Text: "pasted"}}},
		{[]string{"\x1b[200~never ends"}, nil},
	} {
		var d Decoder
		var got []Key
		for _, p := range c.parts {
			got = append(got, d.Feed([]byte(p))...)
		}
		got = append(got, d.Flush()...)
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: %+v, want %+v", c.parts, got, c.want)
		}
	}
}

func TestDecoderBoundsAPaste(t *testing.T) {
	var d Decoder
	got := d.Feed([]byte(pasteStart + strings.Repeat("x", 2*pasteMax) + pasteEnd))
	if len(got) != 1 || len(got[0].Text) != pasteMax {
		t.Fatalf("%d keys, %d bytes", len(got), len(got[0].Text))
	}
}

// feed runs pump over chunks sent gap apart and returns the keys, each with
// its read.
func feed(t *testing.T, gap time.Duration, chunks ...string) []Key {
	t.Helper()
	in := make(chan []byte)
	keys := make(chan Key, 64)
	go pump(in, keys, 20*time.Millisecond, 150*time.Millisecond)
	for i, c := range chunks {
		if i > 0 {
			time.Sleep(gap)
		}
		in <- []byte(c)
	}
	time.Sleep(gap)
	close(in)
	var got []Key
	for k := range keys {
		got = append(got, k)
	}
	return got
}

func withoutReads(ks []Key) []Key {
	out := slices.Clone(ks)
	for i := range out {
		out[i].Read = 0
	}
	return out
}

// pump is the TTY's reader path: what arrives in two reads must not type the
// second half of an arrow key, which would be 'A' (adopt).
func TestPumpJoinsAnArrowSplitAcrossReads(t *testing.T) {
	got := feed(t, 5*time.Millisecond, "\x1b[", "A", "\x1b")
	if !slices.Equal(withoutReads(got), []Key{{Name: KeyUp}, {Name: KeyEsc}}) {
		t.Fatalf("%+v", got)
	}
	if got[0].Read != 2 || got[1].Read != 3 {
		t.Fatalf("reads %+v", got)
	}
}

func TestPumpKeepsAPasteWholeAcrossAPause(t *testing.T) {
	for name, chunks := range map[string][]string{
		"pause in the text":       {"\x1b[200~hello ", "Ay\x1b[201~q"},
		"pause in the end marker": {"\x1b[200~hello Ay\x1b[2", "01~q"},
	} {
		got := withoutReads(feed(t, 60*time.Millisecond, chunks...))
		if !slices.Equal(got, []Key{{Name: KeyPaste, Text: "hello Ay"}, {Rune: 'q'}}) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestPumpDropsTheRestOfAPasteThatWentIdle(t *testing.T) {
	got := withoutReads(feed(t, 200*time.Millisecond, "\x1b[200~abc", "Ay\x1b[201~", "q"))
	if !slices.Equal(got, []Key{{Rune: 'q'}}) {
		t.Fatalf("%+v", got)
	}
}

func TestDecoderSkipsAnOverlongSequenceWhole(t *testing.T) {
	var d Decoder
	got := d.Feed([]byte("\x1b[" + strings.Repeat("1", 2*heldMax)))
	got = append(got, d.Feed([]byte("1;5Ax"))...)
	if !slices.Equal(got, []Key{{Rune: 'x'}}) {
		t.Fatalf("%+v", got)
	}
	if got := Decode([]byte("\x1b[12\x1b[A")); !slices.Equal(got, []Key{{Name: KeyUp}}) {
		t.Fatalf("a sequence cut by another: %+v", got)
	}
}

func TestFlushLeavesAPasteAlone(t *testing.T) {
	var d Decoder
	d.Feed([]byte("\x1b[200~abc"))
	if got := d.Flush(); got != nil || !d.InPaste() {
		t.Fatalf("%+v", got)
	}
	if got := d.Feed([]byte("d\x1b[201~")); !slices.Equal(got, []Key{{Name: KeyPaste, Text: "abcd"}}) {
		t.Fatalf("%+v", got)
	}
}
