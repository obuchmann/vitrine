package main

import "testing"

// Tests share the console of go test, or have none, so nothing is released.
func TestReleaseOwnConsoleKeepsASharedConsole(t *testing.T) {
	if releaseOwnConsole() {
		t.Fatal("released a console shared with other processes")
	}
}
