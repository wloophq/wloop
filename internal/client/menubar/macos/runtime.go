//go:build darwin

package macos

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var frameworksOnce sync.Once

// loadFrameworks makes the AppKit and UserNotifications classes visible to
// objc.GetClass. It runs on the first use, so commands that never show the
// menu bar never load AppKit.
func loadFrameworks() {
	frameworksOnce.Do(func() {
		for _, f := range []string{
			"/System/Library/Frameworks/AppKit.framework/AppKit",
			"/System/Library/Frameworks/UserNotifications.framework/UserNotifications",
		} {
			if _, err := purego.Dlopen(f, purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
				panic(fmt.Sprintf("loading %s: %v", f, err))
			}
		}
	})
}

var sel = objc.RegisterName

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	return class("NSString").Send(sel("stringWithUTF8String:"), &b[0])
}

func goString(id objc.ID) string {
	if id == 0 {
		return ""
	}
	p := objc.Send[*byte](id, sel("UTF8String"))
	if p == nil {
		return ""
	}
	var out []byte
	for ptr := unsafe.Pointer(p); *(*byte)(ptr) != 0; ptr = unsafe.Add(ptr, 1) {
		out = append(out, *(*byte)(ptr))
	}
	return string(out)
}

func nsArray(items ...objc.ID) objc.ID {
	if len(items) == 0 {
		return class("NSArray").Send(sel("array"))
	}
	return class("NSArray").Send(sel("arrayWithObjects:count:"), &items[0], uint(len(items)))
}

// withPool runs fn inside an autorelease pool. Goroutines have none, and what
// is autoreleased without one lives as long as the OS thread, which Go keeps.
func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := class("NSAutoreleasePool").Send(sel("new"))
	defer pool.Send(sel("drain"))
	fn()
}

// callBlock calls a block the system handed in. purego's Block.Invoke only
// knows blocks it created; a block literal keeps its invoke pointer after
// isa, flags and reserved.
func callBlock(b objc.Block, args ...uintptr) {
	p := uintptr(b)
	invoke := *(*uintptr)(unsafe.Add(*(*unsafe.Pointer)(unsafe.Pointer(&p)), 16))
	purego.SyscallN(invoke, append([]uintptr{p}, args...)...)
}

// The pump is one Objective-C object that runs Go functions on the main
// thread, receives menu clicks by tag, and is the application delegate.
var (
	pumpOnce sync.Once
	pump     objc.ID
	pumpMu   sync.Mutex
	queue    []func()
	actions  = map[int]func(){}
	previous map[int]func()
	nextTag  int
	launched func()
)

func setupPump() {
	pumpOnce.Do(func() {
		c, err := objc.RegisterClass("LoopPump", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
			{Cmd: sel("drain:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID) { drain() }},
			{Cmd: sel("clicked:"), Fn: func(_ objc.ID, _ objc.SEL, sender objc.ID) {
				runTag(objc.Send[int](sender, sel("tag")))
			}},
			{Cmd: sel("applicationDidFinishLaunching:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID) {
				if launched != nil {
					launched()
				}
			}},
		})
		if err != nil {
			panic(fmt.Sprintf("registering the main-thread pump: %v", err))
		}
		pump = objc.ID(c).Send(sel("new"))
	})
}

func drain() {
	pumpMu.Lock()
	q := queue
	queue = nil
	pumpMu.Unlock()
	for _, f := range q {
		f()
	}
}

// onMain runs fn on the main thread, after whatever it is doing now.
func onMain(fn func()) {
	pumpMu.Lock()
	queue = append(queue, fn)
	pumpMu.Unlock()
	withPool(func() {
		pump.Send(sel("performSelectorOnMainThread:withObject:waitUntilDone:"), sel("drain:"), objc.ID(0), false)
	})
}

// resetActions starts the tags of a new menu; the tray rebuilds the whole
// menu on every change. Tags never repeat, and the previous menu's stay
// valid, because the menu can be rebuilt while the user has it open.
func resetActions() {
	pumpMu.Lock()
	defer pumpMu.Unlock()
	previous, actions = actions, map[int]func(){}
}

func tagFor(fn func()) int {
	pumpMu.Lock()
	defer pumpMu.Unlock()
	nextTag++
	actions[nextTag] = fn
	return nextTag
}

// runTag runs the action off the main thread, so a click never blocks the
// menu on the daemon.
func runTag(tag int) {
	pumpMu.Lock()
	fn, ok := actions[tag]
	if !ok {
		fn = previous[tag]
	}
	pumpMu.Unlock()
	if fn != nil {
		go fn()
	}
}
