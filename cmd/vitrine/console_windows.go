package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
)

// releaseOwnConsole closes the console window that Windows opens when
// vitrine starts without one, as it does from Task Scheduler at logon. A
// console shared with a shell stays. It returns true if it closed the
// window; output then has to go to a file.
func releaseOwnConsole() bool {
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return false
	}
	ok, _, _ := procFreeConsole.Call()
	return ok != 0
}
