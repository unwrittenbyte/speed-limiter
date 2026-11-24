// SPDX-License-Identifier: GPL-3.0-only
// Windows-only, per-user install/uninstall. No elevation, downloads or services.
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unsafe"
)

//go:embed companion.exe
var payload embed.FS
var extensionID = "agbejnpaacdhklaocalpballabcibmgm"
var buildLabel = "Unwritten Byte"

const hostName = "com.csl.speedlimiter_v1"
const regHost = `HKCU\Software\Google\Chrome\NativeMessagingHosts\` + hostName
const regApp = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\ChromeSpeedLimiterV1`

var user32 = syscall.NewLazyDLL("user32.dll")
var msgBox = user32.NewProc("MessageBoxW")

func message(body string, flags uintptr) uintptr {
	b, _ := syscall.UTF16PtrFromString(body)
	t, _ := syscall.UTF16PtrFromString("Speed Limiter — " + buildLabel)
	r, _, _ := msgBox.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), flags)
	return r
}
func reg(args ...string) error {
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "reg.exe"), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	b, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("registry operation failed: %s (%v)", string(b), e)
	}
	return nil
}
func add(key, name, value string) error {
	a := []string{"ADD", key}
	if name == "" {
		a = append(a, "/ve")
	} else {
		a = append(a, "/v", name)
	}
	a = append(a, "/t", "REG_SZ", "/d", value, "/f")
	return reg(a...)
}
func install(dir string) error {
	if !regexp.MustCompile(`^[a-p]{32}$`).MatchString(extensionID) {
		return fmt.Errorf("invalid extension ID")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	host := filepath.Join(dir, "companion.exe")
	b, err := payload.ReadFile("companion.exe")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "host-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// Verify the embedded executable was written intact before installing it.
	written, err := os.ReadFile(tmp.Name())
	if err != nil {
		return err
	}
	if !bytes.Equal(written, b) {
		return fmt.Errorf("companion file verification failed; retry setup")
	}
	if err = os.Rename(tmp.Name(), host); err != nil {
		return fmt.Errorf("close Chrome completely and try again: %w", err)
	}
	manifest := filepath.Join(dir, "native-host.json")
	data, _ := json.MarshalIndent(map[string]any{"name": hostName, "description": "Chrome Speed Limiter local companion", "path": host, "type": "stdio", "allowed_origins": []string{"chrome-extension://" + extensionID + "/"}}, "", "  ")
	if err = os.WriteFile(manifest, data, 0600); err != nil {
		return err
	}
	if err = add(regHost, "", manifest); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	setup, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	uninstall := filepath.Join(dir, "Setup.exe")
	if !samePath(self, uninstall) {
		if err = os.WriteFile(uninstall, setup, 0700); err != nil {
			return err
		}
	}
	for _, pair := range [][2]string{{"DisplayName", "Speed Limiter Companion"}, {"DisplayVersion", "1.0.6"}, {"Publisher", "Unwritten Byte"}, {"UninstallString", `"` + uninstall + `" --uninstall`}, {"InstallLocation", dir}, {"DisplayIcon", host + ",0"}} {
		if err = add(regApp, pair[0], pair[1]); err != nil {
			return err
		}
	}
	for _, name := range []string{"NoModify", "NoRepair"} {
		if err = reg("ADD", regApp, "/v", name, "/t", "REG_DWORD", "/d", "1", "/f"); err != nil {
			return err
		}
	}
	return nil
}
func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }
func uninstall(dir string) error {
	host := filepath.Join(dir, "companion.exe")
	if err := os.Remove(host); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("close Chrome completely and retry: %w", err)
	}
	if err := reg("DELETE", regHost, "/f"); err != nil {
		if reg("QUERY", regHost) == nil {
			return err
		}
	}
	if err := reg("DELETE", regApp, "/f"); err != nil {
		if reg("QUERY", regApp) == nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(dir, "native-host.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Only delete our known setup executable and empty installation directories.
// Windows cannot normally delete a running image; a short-lived hidden helper
// retries after this process exits. No service or scheduled task is created.
func cleanupSetup(dir string) error {
	setup := filepath.Join(dir, "Setup.exe")
	if err := os.Remove(setup); err == nil || os.IsNotExist(err) {
		os.Remove(dir)
		os.Remove(filepath.Dir(dir))
		return nil
	}
	f, err := os.CreateTemp("", "csl-cleanup-*.cmd")
	if err != nil {
		return err
	}
	script := `@echo off
setlocal DisableDelayedExpansion
for /L %%i in (1,1,60) do (
  del /f /q "%CSL_CLEAN_SETUP%" >nul 2>&1
  if not exist "%CSL_CLEAN_SETUP%" goto clean
  "%SystemRoot%\System32\ping.exe" -n 2 127.0.0.1 >nul
)
goto done
:clean
rd "%CSL_CLEAN_DIR%" >nul 2>&1
rd "%CSL_CLEAN_PARENT%" >nul 2>&1
:done
del /f /q "%~f0" >nul 2>&1
`
	if _, err = f.WriteString(strings.ReplaceAll(script, "\n", "\r\n")); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err = f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	cmdPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmd := exec.Command(cmdPath)
	cmd.Env = append(os.Environ(), "CSL_CLEAN_SETUP="+setup, "CSL_CLEAN_DIR="+dir, "CSL_CLEAN_PARENT="+filepath.Dir(dir), "CSL_CLEAN_SCRIPT="+f.Name())
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000, CmdLine: `"` + cmdPath + `" /D /Q /V:OFF /S /C ""%CSL_CLEAN_SCRIPT%""`}
	if err = cmd.Start(); err != nil {
		os.Remove(f.Name())
		return err
	}
	cmd.Process.Release()
	return nil
}
func main() {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		message("LOCALAPPDATA is unavailable.", 0x10)
		return
	}
	dir := filepath.Join(local, "Chrome Speed Limiter", "v1")
	remove := len(os.Args) > 1 && os.Args[1] == "--uninstall"
	if !remove {
		if message("Install or update the free Speed Limiter Companion 1.0.6?\n\nClose Chrome completely first (including any background Chrome processes). This installs only for your Windows account and adds no background service.\n\nExtension ID: "+extensionID+"\n\nChoose No to cancel.", 0x24) != 6 {
			return
		}
	} else {
		if message("Disconnect proxy, remove the Chrome extension, and close Chrome completely.\n\nUninstall the local companion?", 0x24) != 6 {
			return
		}
	}
	var err error
	if remove {
		err = uninstall(dir)
	} else {
		err = install(dir)
	}
	if err != nil {
		message(err.Error(), 0x10)
		os.Exit(1)
	}
	if remove {
		message("Companion removed. Remaining setup files will be cleaned up automatically after this window closes.\n\nRemove the extension from Chrome if you have not yet done so.", 0x40)
		if err := cleanupSetup(dir); err != nil {
			message("Automatic setup cleanup could not start. You can delete the remaining Setup.exe after this window closes.\n"+err.Error(), 0x30)
		}
	} else {
		message("Speed Limiter Companion 1.0.6 installed successfully. Open Chrome and use the extension's Check Windows companion button.\n\nYou can uninstall from Windows Settings > Apps.", 0x40)
	}
}
