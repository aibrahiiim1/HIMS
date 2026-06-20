// Trust-audit pattern helpers shared by the Trust Audit page and the Action Center.
// Patterns the SYSTEM can act on (a stronger collector exists and was skipped) vs honest-terminal.
export const ACTIONABLE_PATTERNS = new Set([
  'esxi_evidence_vsphere_skipped', 'redfish_bmc_reachable_not_collected',
  'windows_evidence_deep_collect_skipped', 'linux_evidence_ssh_skipped', 'applicable_collector_skipped',
])

export const ACTION_LABEL: Record<string, string> = {
  onboard_esxi: 'Onboard ESXi',
  collect_bmc: 'Collect BMC',
  retry_ssh: 'Retry SSH',
  rerun_windows: 'Re-run Windows ladder',
}
export const ACTION_CONFIRM: Record<string, string> = {
  onboard_esxi: 'Run vSphere collection? On success the host is classified ESXi (virtual_host) and the credential is bound. Nothing changes if it fails.',
  collect_bmc: 'Run Redfish/BMC collection? Hardware/health is collected and the credential bound only on success.',
  retry_ssh: 'Run the SSH/Linux collector with applicable credentials? OS/hardware inventory is collected on success.',
  rerun_windows: 'Run the full Windows ladder (WinRM/PSRP → WMI/DCOM → WSMan/CIM → relay agent)? Reports the winning method or the honest final reason.',
}
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
