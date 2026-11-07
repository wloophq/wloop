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
		purego.RegisterLibFunc(&cf.copyPreferredLanguages, lib, "CFLocaleCopyPreferredLanguages")
		purego.RegisterLibFunc(&cf.arrayGetCount, lib, "CFArrayGetCount")
		purego.RegisterLibFunc(&cf.arrayGetValueAtIndex, lib, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&cf.stringGetCString, lib, "CFStringGetCString")
		purego.RegisterLibFunc(&cf.release, lib, "CFRelease")
		cf.ok = true
	})
	return cf.ok
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
