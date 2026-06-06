package osinv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Windows Native Collector fallback. Legacy Windows hosts (Windows 7 / Server
// 2008 R2, WSMan Stack 2.0) authenticate fine but the Go WinRM library cannot run
// commands (wsman:InvalidSelectors). Native PowerShell Invoke-Command DOES work
// against them, so HIMS delegates collection to an operator-deployed Windows
// helper that runs on a Windows/domain box and uses the native WSMan/PowerShell
// stack. HIMS POSTs {host, username, password} to the helper; the helper returns
// a JSON document in the SAME shape as osinv.Report (read-only inventory). The
// password travels to the trusted helper only — it is never logged here.
//
// Contract (helper endpoint):
//   POST <collector_url>
//   Authorization: Bearer <token>            (optional shared secret)
//   Body: {"host":"172.21.60.172","username":"coralsearesorts\\dpm","password":"..."}
//   200 -> osinv.Report JSON  |  non-200 -> {"error":"..."} (sanitized, no secret)

// NativeCollectorRequest is what HIMS sends the helper. Mode tells the helper
// which transport to use: "winrm-native" (Invoke-Command) or "wmi" (Get-WmiObject
// over DCOM — works even when WinRM is disabled).
type NativeCollectorRequest struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
	Mode     string `json:"mode,omitempty"`
}

// CollectViaNativeCollector delegates deep OS collection of a legacy Windows host
// to the configured Windows Native Collector helper and returns its Report.
func CollectViaNativeCollector(ctx context.Context, collectorURL, token, host, user, pass string, timeout time.Duration) (Report, error) {
	rep, err := postCollector(ctx, collectorURL, token, host, user, pass, timeout, "winrm-native")
	if err != nil {
		return Report{}, err
	}
	rep.Method = "winrm-native"
	return rep, nil
}

// postCollector is the shared HTTP call to a collector helper (Native or WMI):
// POST {host, username, password, mode} → osinv.Report JSON. The password reaches
// the trusted helper only and is never logged. label is used in error messages.
func postCollector(ctx context.Context, collectorURL, token, host, user, pass string, timeout time.Duration, mode string) (Report, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	label := "native collector"
	if mode == "wmi" {
		label = "WMI collector"
	}
	payload, _ := json.Marshal(NativeCollectorRequest{Host: host, Username: user, Password: pass, Mode: mode})
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, collectorURL, bytes.NewReader(payload))
	if err != nil {
		return Report{}, fmt.Errorf("%s: bad URL: %w", label, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return Report{}, fmt.Errorf("%s unreachable: %w", label, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			return Report{}, fmt.Errorf("%s error (%d): %s", label, resp.StatusCode, e.Error)
		}
		return Report{}, fmt.Errorf("%s returned HTTP %d", label, resp.StatusCode)
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return Report{}, fmt.Errorf("%s returned invalid inventory JSON: %w", label, err)
	}
	return rep, nil
}
