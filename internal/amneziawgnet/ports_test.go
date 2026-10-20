package amneziawgnet

import (
	"fmt"
	"io"
	"net"
	"testing"
)

// freeHostPorts returns n distinct ports free right now for both TCP and UDP on every
// interface. A fixed port sits in the ephemeral range, where any process's sockets land.
func freeHostPorts(t *testing.T, n int) []int {
	t.Helper()
	var held []io.Closer
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	ports := make([]int, 0, n)
	for tries := 0; len(ports) < n; tries++ {
		if tries == 100 {
			t.Fatalf("found %d of %d ports free for both TCP and UDP in %d tries", len(ports), n, tries)
		}
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Fatalf("reserve a TCP port: %v", err)
		}
		held = append(held, ln)
		port := ln.Addr().(*net.TCPAddr).Port
		pc, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue // its UDP twin is taken; the TCP side stays held so it is not offered again
		}
		held = append(held, pc)
		ports = append(ports, port)
	}
	return ports
}

func freeHostPort(t *testing.T) int {
	t.Helper()
	return freeHostPorts(t, 1)[0]
}
