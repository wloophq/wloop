package tui

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"
)

// Key is one key press: a printable rune, or a named key with Rune 0. A
// bracketed paste arrives whole as KeyPaste with its Text. Read numbers the
// terminal read the key came in, so a key typed ahead in the same read as
// the one that opened a prompt can be told apart from a deliberate answer.
type Key struct {
	Rune rune
	Name string
	Text string
	Read uint64
}

const (
	KeyUp        = "up"
	KeyDown      = "down"
	KeyEnter     = "enter"
	KeyEsc       = "esc"
	KeyBackspace = "backspace"
	KeyTab       = "tab"
	KeyCtrlC     = "ctrl-c"
	KeyCtrlZ     = "ctrl-z"
	KeyCtrlK     = "ctrl-k"
	KeyCtrlE     = "ctrl-e"
	KeyPaste     = "paste"
)

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
	pasteMax   = 4096
	// heldMax bounds an escape sequence that never ends.
	heldMax = 64
)

type decodeMode int

const (
	normal decodeMode = iota
	pasting
	// discarding drops the rest of a paste that went idle, up to its end
	// marker, so its tail never becomes keys.
	discarding
	// skipping drops an over-long escape sequence up to its final byte.
	skipping
)

// Decoder turns what a raw terminal sends into keys, across reads: an escape
// sequence or a UTF-8 rune split between two reads is held for the next one,
// so its second half never arrives as typed text. Escape sequences it does
// not know are dropped whole.
type Decoder struct {
	held      []byte
	paste     []byte
	mode      decodeMode
	discarded int
}

// Feed decodes b after the bytes held from the previous read.
func (d *Decoder) Feed(b []byte) []Key {
	buf := append(d.held, b...)
	d.held = nil
	var out []Key
	for len(buf) > 0 {
		switch d.mode {
		case pasting, discarding:
			i := bytes.Index(buf, []byte(pasteEnd))
			if i < 0 {
				keep := partialSuffix(buf, pasteEnd)
				d.consumePaste(buf[:len(buf)-keep])
				if d.mode != normal {
					d.held = bytes.Clone(buf[len(buf)-keep:])
				}
				return out
			}
			d.consumePaste(buf[:i])
			if d.mode == pasting {
				out = append(out, Key{Name: KeyPaste, Text: strings.ToValidUTF8(string(d.paste), "")})
			}
			d.paste, d.mode = nil, normal
			buf = buf[i+len(pasteEnd):]
			continue
		case skipping:
			i := bytes.IndexFunc(buf, func(r rune) bool { return r >= 0x40 && r <= 0x7e })
			if i < 0 {
				return out
			}
			d.mode = normal
			buf = buf[i+1:]
			continue
		}
		k, n, complete := next(buf)
		if !complete {
			switch {
			case len(buf) <= heldMax:
				d.held = bytes.Clone(buf)
			case bytes.HasPrefix(buf, []byte("\x1b[")):
				d.mode = skipping
			}
			return out
		}
		buf = buf[n:]
		switch {
		case k.Name == pasteStart:
			d.mode = pasting
		case k != Key{}:
			out = append(out, k)
		}
	}
	return out
}

// Holding reports whether bytes wait for the rest of an escape sequence or
// a rune: Flush ends the wait.
func (d *Decoder) Holding() bool { return d.mode == normal && len(d.held) > 0 }

// InPaste reports whether a paste has not ended: Expire ends the wait.
func (d *Decoder) InPaste() bool { return d.mode == pasting || d.mode == discarding }

// Flush is called when no more bytes came within a key's time: a lone ESC is
// the Esc key, and anything else held is dropped. A paste is not touched.
func (d *Decoder) Flush() []Key {
	if d.mode != normal {
		return nil
	}
	held := d.held
	d.held = nil
	if string(held) == "\x1b" {
		return []Key{{Name: KeyEsc}}
	}
	return nil
}

// Expire is called when a paste went idle: what came so far is dropped and
// so is the rest, up to the end marker. A discard that goes idle too ends.
func (d *Decoder) Expire() {
	switch d.mode {
	case pasting:
		d.mode, d.paste, d.discarded = discarding, nil, 0
	case discarding:
		d.mode, d.held = normal, nil
	}
}

func (d *Decoder) consumePaste(b []byte) {
	if d.mode == pasting {
		d.paste = append(d.paste, b[:min(len(b), pasteMax-len(d.paste))]...)
		return
	}
	if d.discarded += len(b); d.discarded > pasteMax {
		d.mode, d.held = normal, nil
	}
}

// partialSuffix is the length of the longest end of b that begins marker.
func partialSuffix(b []byte, marker string) int {
	for n := min(len(b), len(marker)-1); n > 0; n-- {
		if string(b[len(b)-n:]) == marker[:n] {
			return n
		}
	}
	return 0
}

// next decodes the key at the start of b and how many bytes it took;
// complete is false when b ends inside it.
func next(b []byte) (k Key, n int, complete bool) {
	switch c := b[0]; {
	case c == 0x1b:
		return escape(b)
	case c == '\r' || c == '\n':
		return Key{Name: KeyEnter}, 1, true
	case c == 0x7f || c == 0x08:
		return Key{Name: KeyBackspace}, 1, true
	case c == '\t':
		return Key{Name: KeyTab}, 1, true
	case c == 0x03:
		return Key{Name: KeyCtrlC}, 1, true
	case c == 0x1a:
		return Key{Name: KeyCtrlZ}, 1, true
	case c == 0x0b:
		return Key{Name: KeyCtrlK}, 1, true
	case c == 0x05:
		return Key{Name: KeyCtrlE}, 1, true
	case c < 0x20:
		return Key{}, 1, true
	}
	if !utf8.FullRune(b) {
		return Key{}, 0, false
	}
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError {
		return Key{}, size, true
	}
	return Key{Rune: r}, size, true
}

func escape(b []byte) (Key, int, bool) {
	if len(b) < 2 {
		return Key{}, 0, false
	}
	switch b[1] {
	case '[':
		for i := 2; i < len(b); i++ {
			if b[i] == 0x1b {
				return Key{}, i, true
			}
			if b[i] < 0x40 || b[i] > 0x7e {
				continue
			}
			switch string(b[:i+1]) {
			case "\x1b[A":
				return Key{Name: KeyUp}, i + 1, true
			case "\x1b[B":
				return Key{Name: KeyDown}, i + 1, true
			case pasteStart:
				return Key{Name: pasteStart}, i + 1, true
			}
			return Key{}, i + 1, true
		}
		return Key{}, 0, false
	case 'O':
		if len(b) < 3 {
			return Key{}, 0, false
		}
		switch b[2] {
		case 'A':
			return Key{Name: KeyUp}, 3, true
		case 'B':
			return Key{Name: KeyDown}, 3, true
		}
		return Key{}, 3, true
	case 0x1b:
		return Key{Name: KeyEsc}, 1, true
	}
	// Alt with a key is ignored, so Alt+Backspace in a prompt does not
	// cancel it.
	if !utf8.FullRune(b[1:]) {
		return Key{}, 0, false
	}
	_, size := utf8.DecodeRune(b[1:])
	return Key{}, 1 + size, true
}

// Decode reads the keys in b as one whole input.
func Decode(b []byte) []Key {
	var d Decoder
	return append(d.Feed(b), d.Flush()...)
}

// pump decodes the chunks a terminal reader sends into keys, numbering each
// key with its read. Bytes held for an escape sequence are flushed after
// escWait with nothing more, so a lone ESC is the Esc key only once no
// sequence can follow it; a paste that stays idle for pasteIdle expires.
func pump(chunks <-chan []byte, keys chan<- Key, escWait, pasteIdle time.Duration) {
	defer close(keys)
	var d Decoder
	var esc, paste <-chan time.Time
	var read uint64
	send := func(ks []Key) {
		for _, k := range ks {
			k.Read = read
			keys <- k
		}
	}
	arm := func() {
		esc, paste = nil, nil
		if d.Holding() {
			esc = time.After(escWait)
		}
		if d.InPaste() {
			paste = time.After(pasteIdle)
		}
	}
	for {
		select {
		case b, ok := <-chunks:
			if !ok {
				send(d.Flush())
				return
			}
			read++
			send(d.Feed(b))
			arm()
		case <-esc:
			send(d.Flush())
			arm()
		case <-paste:
			d.Expire()
			arm()
		}
	}
}
