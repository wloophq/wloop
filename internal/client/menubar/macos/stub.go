//go:build !darwin

package macos

import "github.com/wloophq/wloop/internal/client/menubar"

func Available() bool { return false }

func Run(string, func(menubar.Tray, menubar.Notifier)) { panic("the menu bar needs macOS") }

func Quit() {}
