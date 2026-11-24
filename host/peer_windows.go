//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Match the local TCP client to the Chrome browser that launched this native
// host, or a live descendant using the same Chrome image. Never trust merely
// a loopback address, a caller-supplied header, or a process name alone.
var tcpTable = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")
var queryImage = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")

func parents() map[uint32]uint32 {
	h, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(h)
	var p syscall.ProcessEntry32
	p.Size = uint32(unsafe.Sizeof(p))
	out := map[uint32]uint32{}
	for err = syscall.Process32First(h, &p); err == nil; err = syscall.Process32Next(h, &p) {
		out[p.ProcessID] = p.ParentProcessID
	}
	return out
}
func processInfo(pid uint32) (syscall.Handle, string, int64) {
	h, err := syscall.OpenProcess(0x1000|0x100000, false, pid) // query limited + synchronize
	if err != nil {
		return 0, "", 0
	}
	b := make([]uint16, 32768)
	n := uint32(len(b))
	r, _, _ := queryImage.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&n)))
	var created, exited, k, u syscall.Filetime
	if r == 0 || n == 0 || n > uint32(len(b)) || syscall.GetProcessTimes(h, &created, &exited, &k, &u) != nil {
		syscall.CloseHandle(h)
		return 0, "", 0
	}
	return h, filepath.Clean(syscall.UTF16ToString(b[:n])), created.Nanoseconds()
}
func alive(h syscall.Handle) bool {
	r, err := syscall.WaitForSingleObject(h, 0)
	return err == nil && r == 258
}
func peerOwner(c net.Conn) uint32 {
	local, ok := c.LocalAddr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	remote, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	li, ri := local.IP.To4(), remote.IP.To4()
	if li == nil || ri == nil || !local.IP.IsLoopback() || !remote.IP.IsLoopback() {
		return 0
	}
	var n uint32
	r, _, _ := tcpTable.Call(0, uintptr(unsafe.Pointer(&n)), 0, 2, 4, 0) // AF_INET, OWNER_PID_CONNECTIONS
	if r != 122 || n < 4 || n > 16<<20 {
		return 0
	}
	for i := 0; i < 3; i++ {
		b := make([]byte, n)
		r, _, _ := tcpTable.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&n)), 0, 2, 4, 0)
		if r == 122 && n >= 4 && n <= 16<<20 {
			continue
		}
		if r != 0 || n < 4 || int(n) > len(b) {
			return 0
		}
		count := int(binary.LittleEndian.Uint32(b[:4]))
		if count > (int(n)-4)/24 {
			return 0
		}
		for j := 0; j < count; j++ {
			row := b[4+j*24 : 4+(j+1)*24]
			if binary.LittleEndian.Uint32(row[:4]) != 5 {
				continue
			} // ESTABLISHED
			if string(row[4:8]) == string(ri) && int(binary.BigEndian.Uint16(row[8:10])) == remote.Port && string(row[12:16]) == string(li) && int(binary.BigEndian.Uint16(row[16:18])) == local.Port {
				return binary.LittleEndian.Uint32(row[20:24])
			}
		}
		return 0
	}
	return 0
}
func browserPeerCheck() (func(net.Conn) bool, func(), bool) {
	m := parents()
	pid := uint32(os.Getpid())
	var root uint32
	var rootHandle syscall.Handle
	var rootPath string
	var rootTime int64
	// Native messaging may use an intermediate cmd.exe; inspect ancestry.
	for i := 0; i < 8; i++ {
		p := m[pid]
		if p == 0 || p == pid {
			break
		}
		pid = p
		h, path, created := processInfo(pid)
		if h == 0 {
			break
		}
		if strings.EqualFold(filepath.Base(path), "chrome.exe") && alive(h) {
			root = pid
			rootHandle = h
			rootPath = path
			rootTime = created
			break
		}
		syscall.CloseHandle(h)
	}
	if root == 0 {
		return func(net.Conn) bool { return false }, func() {}, false
	}
	var mu sync.RWMutex
	closed := false
	return func(c net.Conn) bool {
		mu.RLock()
		defer mu.RUnlock()
		if closed || !alive(rootHandle) {
			return false
		}
		pid := peerOwner(c)
		if pid == 0 {
			return false
		}
		if pid == root {
			return true
		}
		m := parents()
		h, path, created := processInfo(pid)
		if h == 0 {
			return false
		}
		defer syscall.CloseHandle(h)
		if !alive(h) || created < rootTime || !strings.EqualFold(path, rootPath) {
			return false
		}
		// Chrome's network service is normally a direct child. Also allow a
		// chain of live same-image descendants with monotonic creation times.
		for i := 0; i < 8; i++ {
			p := m[pid]
			if p == root {
				return alive(rootHandle)
			}
			if p == 0 || p == pid {
				return false
			}
			ph, pp, pt := processInfo(p)
			if ph == 0 {
				return false
			}
			ok := alive(ph) && strings.EqualFold(pp, rootPath) && pt >= rootTime && pt <= created
			syscall.CloseHandle(ph)
			if !ok {
				return false
			}
			pid = p
			created = pt
		}
		return false
	}, func() { mu.Lock(); defer mu.Unlock(); closed = true; syscall.CloseHandle(rootHandle) }, true
}
