//go:build !windows

package main

// Non-Windows builds never run under a Windows Service Control Manager; Linux
// uses systemd (Type=simple), which runs the binary in foreground mode and
// delivers SIGTERM on stop — handled in main().
func runUnderServiceManager() bool { return false }

func runAsService() {} // unreachable on non-Windows
