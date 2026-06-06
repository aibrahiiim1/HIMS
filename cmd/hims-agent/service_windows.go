//go:build windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
)

// runUnderServiceManager reports whether the process was launched by the Windows
// Service Control Manager (vs. an interactive console).
func runUnderServiceManager() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// himsService adapts the agent run loop to the Windows Service Control Manager:
// it reports Running, runs the agent until a Stop/Shutdown arrives, then cancels
// the run context and reports Stopped — so Services.msc / sc.exe can start and
// stop it cleanly.
type himsService struct{}

func (m *himsService) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = newAgentFromEnv().run(ctx); close(done) }()

	s <- svc.Status{State: svc.Running, Accepts: accepted}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			logln("service stop requested")
			s <- svc.Status{State: svc.StopPending}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
			return false, 0
		default:
			logf("unexpected service control request #%d", c.Cmd)
		}
	}
	return false, 0
}

func runAsService() {
	setupServiceLogging()
	logf("HIMS Relay Agent %s starting as Windows service", agentVersion)
	if err := svc.Run(serviceName, &himsService{}); err != nil {
		logln("service run error:", err)
		os.Exit(1)
	}
}

// setupServiceLogging routes agent output to a log file under ProgramData so the
// operator can read it without a console (the installer surfaces this path).
func setupServiceLogging() {
	dir := os.Getenv("HIMS_AGENT_LOG_DIR")
	if dir == "" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		dir = filepath.Join(pd, "HIMS", "RelayAgent", "logs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		out = f
	}
}
