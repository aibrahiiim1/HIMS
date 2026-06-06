//go:build windows

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
)

// serviceName is the Windows service key; the install script sets the display
// name "HIMS API". Keep this stable — uninstall/restart scripts reference it.
const serviceName = "HIMSAPI"

// runUnderServiceManager reports whether the process was launched by the Windows
// Service Control Manager (vs. an interactive console).
func runUnderServiceManager() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// himsAPIService adapts run() to the Windows SCM: report Running, serve until a
// Stop/Shutdown arrives, then cancel the run context for a graceful drain.
type himsAPIService struct{ logPath string }

func (m *himsAPIService) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, "windows-service", m.logPath) }()

	s <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				slog.Info("service stop requested")
				s <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case err := <-done:
			// run() returned on its own (e.g. startup guard refused to start).
			if err != nil {
				slog.Error("hims-api service exited", "error", err)
				return false, 1
			}
			return false, 0
		}
	}
}

func runAsService() {
	logPath := setupServiceLogging()
	slog.Info("hims-api starting as Windows service", "version", version)
	if err := svc.Run(serviceName, &himsAPIService{logPath: logPath}); err != nil {
		slog.Error("service run error", "error", err)
		os.Exit(1)
	}
}

// setupServiceLogging routes slog JSON to a file under ProgramData so the service
// has a known, readable log location (no console). Returns the log file path.
func setupServiceLogging() string {
	dir := os.Getenv("HIMS_LOG_DIR")
	if dir == "" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		dir = filepath.Join(pd, "HIMS", "API", "logs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "hims-api.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ""
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(f, nil)))
	return path
}
