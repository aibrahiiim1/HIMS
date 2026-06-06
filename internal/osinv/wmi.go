package osinv

import (
	"context"
	"net"
	"strings"
	"time"
)

// WMI/DCOM legacy Windows fallback. For Windows 7 / Server 2008 R2 hosts where
// WinRM is disabled or the Go WinRM library is incompatible, WMI over DCOM/RPC
// (TCP 135 + dynamic ports) is the classic management path. A correct pure-Go
// DCOM/WMI client is a heavy, hard-to-verify dependency, so — exactly like the
// Windows Native Collector — HIMS delegates the actual WMI collection to an
// operator-deployed WMI collector helper (PowerShell Get-WmiObject over DCOM,
// which works even when WinRM is off). HIMS itself does the cheap, real reach-
// ability probe (RPC endpoint mapper on 135) and classifies failures precisely.
//
// Reuses the osinv.Report JSON contract; the WMI helper returns the same shape.

// WMI/DCOM failure categories (also the credential-test categories). These are
// distinct from a wrong password — a credential is only "bad" on wmi_auth_failed.
const (
	WMIDcomUnreachable      = "dcom_unreachable"      // RPC/DCOM endpoint mapper (135) not reachable
	WMIRpcUnreachable       = "rpc_unreachable"       // RPC dynamic range unreachable after EPM
	WMIAccessDenied         = "wmi_access_denied"     // authenticated but namespace/DCOM access denied
	WMIAuthFailed           = "wmi_auth_failed"       // credential rejected
	WMIFirewallBlocked      = "firewall_blocked"      // 135 filtered (timeout, not refused)
	WMINamespaceUnavailable = "namespace_unavailable" // root\cimv2 not available
	WMIUnsupported          = "unsupported"           // no WMI collector configured / not a Windows host
	WMISuccess              = "success"
)

// WMIProbeReachable does a cheap TCP probe of the DCOM/RPC endpoint mapper (135)
// to tell whether WMI/DCOM is even possible before testing a credential. A
// refused connection ≠ filtered: refused → dcom_unreachable; timeout → firewall.
func WMIProbeReachable(ctx context.Context, host string, timeout time.Duration) (reachable bool, category, detail string) {
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(cctx, "tcp", net.JoinHostPort(host, "135"))
	if err == nil {
		_ = conn.Close()
		return true, WMISuccess, "DCOM/RPC endpoint mapper reachable on 135"
	}
	e := strings.ToLower(err.Error())
	switch {
	case strings.Contains(e, "refused"):
		return false, WMIDcomUnreachable, "DCOM/RPC (135) connection refused — RPC service not listening"
	case strings.Contains(e, "timeout") || strings.Contains(e, "deadline") || strings.Contains(e, "i/o timeout"):
		return false, WMIFirewallBlocked, "DCOM/RPC (135) timed out — likely firewall-blocked"
	case strings.Contains(e, "no route") || strings.Contains(e, "no such host") || strings.Contains(e, "unreachable"):
		return false, WMIRpcUnreachable, "host unreachable from the collector"
	default:
		return false, WMIDcomUnreachable, "DCOM/RPC (135) not reachable"
	}
}

// ClassifyWMIError maps a WMI collector error to a precise category + detail.
// Used for both the credential test and the collection path. No secrets.
func ClassifyWMIError(err error) (category, detail string) {
	if err == nil {
		return WMISuccess, "WMI collected"
	}
	e := strings.ToLower(err.Error())
	switch {
	case strings.Contains(e, "access is denied") || strings.Contains(e, "access denied") || strings.Contains(e, "0x80070005"):
		return WMIAccessDenied, "authenticated but WMI/DCOM access was denied (DCOM launch/activation or namespace permissions)"
	case strings.Contains(e, "logon failure") || strings.Contains(e, "auth") || strings.Contains(e, "0x8007052e") || strings.Contains(e, "bad username or password"):
		return WMIAuthFailed, "WMI authentication rejected"
	case strings.Contains(e, "rpc server is unavailable") || strings.Contains(e, "0x800706ba") || strings.Contains(e, "rpc"):
		return WMIRpcUnreachable, "RPC server unavailable (DCOM/RPC ports blocked or service stopped)"
	case strings.Contains(e, "invalid namespace") || strings.Contains(e, "cimv2"):
		return WMINamespaceUnavailable, "WMI namespace root\\cimv2 unavailable"
	case strings.Contains(e, "refused"):
		return WMIDcomUnreachable, "DCOM/RPC connection refused"
	case strings.Contains(e, "timeout") || strings.Contains(e, "deadline"):
		return WMIFirewallBlocked, "DCOM/RPC timed out — likely firewall-blocked"
	default:
		return "wmi_error", strings.TrimSpace(err.Error())
	}
}

// CollectViaWMICollector delegates WMI/DCOM collection of a legacy Windows host
// to the configured WMI collector helper (same JSON contract as the Native
// Collector). Marks provenance Method="wmi". The password reaches the trusted
// helper only and is never logged.
func CollectViaWMICollector(ctx context.Context, collectorURL, token, host, user, pass string, timeout time.Duration) (Report, error) {
	rep, err := postCollector(ctx, collectorURL, token, host, user, pass, timeout, "wmi")
	if err != nil {
		return Report{}, err
	}
	rep.Method = "wmi"
	return rep, nil
}
