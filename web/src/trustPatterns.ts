// Trust-audit pattern helpers shared by the Trust Audit page and the Action Center.
// Patterns the SYSTEM can act on (a stronger collector exists and was skipped) vs honest-terminal.
export const ACTIONABLE_PATTERNS = new Set([
  'esxi_evidence_vsphere_skipped', 'redfish_bmc_reachable_not_collected',
  'windows_evidence_deep_collect_skipped', 'linux_evidence_ssh_skipped', 'applicable_collector_skipped',
])

export const PATTERN_LABEL: Record<string, string> = {
  esxi_evidence_vsphere_skipped: 'ESXi evidence — vSphere skipped',
  redfish_bmc_reachable_not_collected: 'Redfish/BMC reachable — not collected',
  windows_evidence_deep_collect_skipped: 'Windows evidence — deep (WMI/WinRM) skipped',
  linux_evidence_ssh_skipped: 'Linux/SSH evidence — SSH skipped',
  applicable_collector_skipped: 'Applicable collector skipped',
  managed_shallow_no_deeper_evidence: 'Managed shallow — no deeper evidence (honest)',
  web_only_no_deeper_evidence: 'Web-only — no deeper evidence (honest)',
  credential_failed_real: 'Credential failed (honest)',
  authenticated_but_access_denied: 'Auth OK but access denied (host-side)',
  unidentified_no_working_collector: 'Unidentified — no collector worked (honest)',
}
