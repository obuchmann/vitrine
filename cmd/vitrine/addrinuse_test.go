package main

import (
	"errors"
	"net"
	"testing"
)

func TestIsAddrInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, err = net.Listen("tcp", ln.Addr().String())
	if err == nil {
		t.Fatal("second listen on the same port succeeded")
	}
	if !isAddrInUse(err) {
		t.Fatalf("isAddrInUse(%v) = false", err)
	}
	if isAddrInUse(errors.New("other")) {
		t.Fatal("isAddrInUse reports an unrelated error")
	}
}
