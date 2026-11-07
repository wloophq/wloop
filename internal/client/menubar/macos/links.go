//go:build darwin

package macos

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego/objc"
)

// links hands the deep links the system opens to one taker, in order, off
// the main thread; links before a taker are kept.
type links struct {
	mu      sync.Mutex
	pending []string
	queue   chan string
}

func (l *links) receive(link string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.queue == nil {
		l.pending = append(l.pending, link)
		return
	}
	l.queue <- link
}

func (l *links) take(fn func(string)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.queue != nil {
		return
	}
	l.queue = make(chan string, 64)
	for _, link := range l.pending {
		l.queue <- link
	}
	l.pending = nil
	go func() {
		for link := range l.queue {
			fn(link)
		}
	}()
}

var opened = &links{}

// linkTray is the tray of the menu bar running alone, which takes the app's
// links; under a window host, the host takes them.
type linkTray struct{ *tray }

func (linkTray) OnLink(fn func(string)) { opened.take(fn) }

const (
	keyDirectObject = 0x2d2d2d2d // '----'
	internetEvent   = 0x4755524c // 'GURL', the class and the id
)

// listenForLinks asks AppKit for the get-URL Apple events; call it before
// the app runs, so a link that launches the app is not lost.
func listenForLinks() {
	c, err := objc.RegisterClass("LoopLinks", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: sel("handleGetURLEvent:withReplyEvent:"), Fn: func(_ objc.ID, _ objc.SEL, event, _ objc.ID) {
			if s := event.Send(sel("paramDescriptorForKeyword:"), uint32(keyDirectObject)).Send(sel("stringValue")); s != 0 {
				opened.receive(goString(s))
			}
		}},
	})
	if err != nil {
		panic(fmt.Sprintf("registering the link handler: %v", err))
	}
	class("NSAppleEventManager").Send(sel("sharedAppleEventManager")).Send(sel("setEventHandler:andSelector:forEventClass:andEventID:"),
		objc.ID(c).Send(sel("new")), sel("handleGetURLEvent:withReplyEvent:"), uint32(internetEvent), uint32(internetEvent))
}
