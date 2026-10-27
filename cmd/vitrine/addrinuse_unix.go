//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// isAddrInUse reports whether a listen failed because the port is taken.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
