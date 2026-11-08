//go:build darwin

package locale

import (
	"sync"

	"github.com/ebitengine/purego"
)

const coreFoundation = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"

// kCFStringEncodingUTF8 is CoreFoundation's code for UTF-8.
const kCFStringEncodingUTF8 = 0x08000100

var cf struct {
	once                   sync.Once
	ok                     bool
	copyPreferredLanguages func() uintptr
	arrayGetCount          func(uintptr) int
	arrayGetValueAtIndex   func(uintptr, int) uintptr
	stringGetCString       func(uintptr, *byte, int, uint32) bool
	release                func(uintptr)
}

func loadCF() bool {
	cf.once.Do(func() {
		lib, err := purego.Dlopen(coreFoundation, purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return
		}
		cf.ok = bind(lib, map[string]any{
			"CFLocaleCopyPreferredLanguages": &cf.copyPreferredLanguages, "CFArrayGetCount": &cf.arrayGetCount,
			"CFArrayGetValueAtIndex": &cf.arrayGetValueAtIndex, "CFStringGetCString": &cf.stringGetCString, "CFRelease": &cf.release,
		}, purego.Dlsym)
	})
	return cf.ok
}

// bind points each function of fns at its symbol in lib; false, binding none, when lookup misses one.
func bind(lib uintptr, fns map[string]any, lookup func(lib uintptr, name string) (uintptr, error)) bool {
	syms := map[string]uintptr{}
	for name := range fns {
		sym, err := lookup(lib, name)
		if err != nil || sym == 0 {
			return false
		}
		syms[name] = sym
	}
	for name, fn := range fns {
		purego.RegisterFunc(fn, syms[name])
	}
	return true
}

// Preferred are the system's preferred languages, most preferred first: macOS's list, else the environment's.
func Preferred() []string {
	if langs := macLanguages(); len(langs) > 0 {
		return langs
	}
	return environment()
}

func macLanguages() []string {
	if !loadCF() {
		return nil
	}
	arr := cf.copyPreferredLanguages()
	if arr == 0 {
		return nil
	}
	defer cf.release(arr)
	var out []string
	buf := make([]byte, 128)
	for i := range cf.arrayGetCount(arr) {
		s := cf.arrayGetValueAtIndex(arr, i)
		if s == 0 || !cf.stringGetCString(s, &buf[0], len(buf), kCFStringEncodingUTF8) {
			continue
		}
		n := 0
		for n < len(buf) && buf[n] != 0 {
			n++
		}
		out = append(out, string(buf[:n]))
	}
	return out
}
