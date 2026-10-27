package main

import (
	"errors"
	"syscall"
)

// wsaeaddrinuse is the Winsock error for a taken port. syscall.EADDRINUSE is
// an invented errno on Windows that a listen never returns.
const wsaeaddrinuse = syscall.Errno(10048)

// isAddrInUse reports whether a listen failed because the port is taken.
func isAddrInUse(err error) bool {
	return errors.Is(err, wsaeaddrinuse)
}
