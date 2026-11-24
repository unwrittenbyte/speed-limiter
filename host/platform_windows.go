//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"errors"
	"syscall"
	"unsafe"
)

// Detect explicit Windows Internet Options proxies or PAC URLs. WPAD and
// enterprise/VPN networking can still be invisible; the UI discloses this.
func checkSystemProxy() error {
	type config struct {
		autoDetect int32
		pac        *uint16
		proxy      *uint16
		bypass     *uint16
	}
	var c config
	winhttp := syscall.NewLazyDLL("winhttp.dll").NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	free := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalFree")
	r, _, _ := winhttp.Call(uintptr(unsafe.Pointer(&c)))
	for _, p := range []*uint16{c.pac, c.proxy, c.bypass} {
		if p != nil {
			defer free.Call(uintptr(unsafe.Pointer(p)))
		}
	}
	if r == 0 {
		return nil
	}
	if (c.pac != nil && *c.pac != 0) || (c.proxy != nil && *c.proxy != 0) {
		return errors.New("Windows has an explicit system proxy or PAC configured. Limiter not started.")
	}
	return nil
}
