// Package osinv collects deep operating-system inventory from authenticated
// hosts: Windows over WinRM/PowerShell (Get-CimInstance — WMI data without
// DCOM, which is not viable pure-Go from Linux), Linux over SSH. The collectors
// run a small set of commands through an injected Runner and parse the output
// into the transport-neutral Report below. Parsing is pure (unit-tested against
// captured output); only the Runner touches the network.
//
// Honesty: a field is only populated when the host actually returned it. The
// persistence + UI layers render absent sections as "Not collected yet".
package osinv

import "context"

// Runner executes one command/script on a remote host and returns stdout.
// winrm and ssh runners both satisfy this shape.
type Runner interface {
	Run(ctx context.Context, command string) (string, error)
}

// Report is the full deep-inventory result for one host. Dates are kept as the
// raw strings the host emitted (ISO-8601 where we control the format) and parsed
// to time.Time at persistence — osinv stays transport/format-neutral.
type Report struct {
	Method    string        `json:"method"` // "winrm" | "ssh"
	Identity  Identity      `json:"identity"`
	OS        OSInfo        `json:"os"`
	Hardware  Hardware      `json:"hardware"`
	Disks     []Disk        `json:"disks"`
	Nics      []Nic         `json:"nics"`
	Services  []Service     `json:"services"`
	Processes []Process     `json:"processes"`
	Software  []Software    `json:"software"`
	Events    *EventSummary `json:"events,omitempty"`
}

type Identity struct {
	Hostname            string `json:"hostname"`
	FQDN                string `json:"fqdn"`
	Domain              string `json:"domain"`
	Workgroup           string `json:"workgroup"`
	LoggedOnUser        string `json:"logged_on_user"`
	ADDistinguishedName string `json:"ad_distinguished_name"`
	ADOUPath            string `json:"ad_ou_path"`
}

type OSInfo struct {
	Caption       string `json:"caption"`
	Version       string `json:"version"`
	Build         string `json:"build"`
	Edition       string `json:"edition"`
	Arch          string `json:"arch"`
	Kernel        string `json:"kernel"`
	InstallDate   string `json:"install_date"`
	LastBoot      string `json:"last_boot"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Timezone      string `json:"timezone"`
}

type Hardware struct {
	Manufacturer   string `json:"manufacturer"`
	Model          string `json:"model"`
	Serial         string `json:"serial"`
	AssetTag       string `json:"asset_tag"`
	BIOSVersion    string `json:"bios_version"`
	BIOSDate       string `json:"bios_date"`
	CPUModel       string `json:"cpu_model"`
	CPUSockets     int    `json:"cpu_sockets"`
	CPUCores       int    `json:"cpu_cores"`
	RAMTotalBytes  int64  `json:"ram_total_bytes"`
	RAMSlots       int    `json:"ram_slots"`
	SwapTotalBytes int64  `json:"swap_total_bytes"`
}

type Disk struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Serial     string `json:"serial"`
	Filesystem string `json:"filesystem"`
	Health     string `json:"health"`
	SizeBytes  int64  `json:"size_bytes"`
	TotalBytes int64  `json:"total_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
}

type Nic struct {
	Name          string `json:"name"`
	MAC           string `json:"mac"`
	IPAddresses   string `json:"ip_addresses"` // comma-separated
	Gateway       string `json:"gateway"`
	DNSServers    string `json:"dns_servers"` // comma-separated
	DHCPEnabled   bool   `json:"dhcp_enabled"`
	LinkSpeedMbps int64  `json:"link_speed_mbps"`
}

type Service struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	StartType   string `json:"start_type"`
	Account     string `json:"account"`
	Description string `json:"description"`
}

type Process struct {
	Name      string  `json:"name"`
	PID       int     `json:"pid"`
	CPUPct    float64 `json:"cpu_pct"`
	MemBytes  int64   `json:"mem_bytes"`
	StartTime string  `json:"start_time"`
}

type Software struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Publisher   string `json:"publisher"`
	Arch        string `json:"arch"`
	InstallDate string `json:"install_date"`
}

type EventSummary struct {
	Critical24h  int    `json:"critical_24h"`
	Error24h     int    `json:"error_24h"`
	Warning24h   int    `json:"warning_24h"`
	LastCritical string `json:"last_critical"`
}
