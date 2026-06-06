//go:build !windows

package main

import "context"

// Non-Windows builds never run under a Windows Service Control Manager.
func runUnderServiceManager() bool { return false }

// runAsService is unreachable on non-Windows (runUnderServiceManager is false),
// but is defined so main.go compiles on every platform. It just runs console.
func runAsService() { _ = newAgentFromEnv().run(context.Background()) }
