//go:build !windows

package main

// releaseOwnConsole does nothing: only Windows opens a window for a
// background process.
func releaseOwnConsole() bool { return false }
