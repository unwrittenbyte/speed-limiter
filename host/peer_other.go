//go:build !windows

// SPDX-License-Identifier: GPL-3.0-only
package main

import "net"

func browserPeerCheck() (func(net.Conn) bool, func(), bool) {
	return func(net.Conn) bool { return false }, func() {}, false
}
