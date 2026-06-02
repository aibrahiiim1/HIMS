package mibs

// FORTINET-FORTIGATE-MIB OIDs. Every value here was validated against a
// FortiGate-exported MIB during the prior NIMS project; the comments record
// the lessons so they aren't re-learned the hard way.
const (
	FortinetEnterprise = "1.3.6.1.4.1.12356"

	// System scalars (.101.4.1.x.0)
	FgSysVersion   = "1.3.6.1.4.1.12356.101.4.1.1.0" // "v7.4.11,build..." → parse x.y.z
	FgSysCpuUsage  = "1.3.6.1.4.1.12356.101.4.1.3.0" // percent (genuine)
	FgSysMemUsage  = "1.3.6.1.4.1.12356.101.4.1.4.0" // percent (genuine)
	FgSysDiskUsage = "1.3.6.1.4.1.12356.101.4.1.6.0" // MEGABYTES, not percent!
	FgSysDiskCap   = "1.3.6.1.4.1.12356.101.4.1.7.0" // MEGABYTES (total)
	FgSysSesCount  = "1.3.6.1.4.1.12356.101.4.1.8.0" // active sessions

	// High availability (.101.13)
	FgHaSystemMode = "1.3.6.1.4.1.12356.101.13.1.1.0" // 1=standalone 2=AA 3=AP
	FgHaGroupName  = "1.3.6.1.4.1.12356.101.13.1.7.0" // fgHaInfo 7 (NOT 3 = fgHaPriority!)
	FgHaStatsEntry = "1.3.6.1.4.1.12356.101.13.2.1.1" // single index; cols below

	// VPN IPsec tunnels (.101.12.2.2.1) — COMPOSITE index {tunnel, phase2}.
	FgVpnTunEntry = "1.3.6.1.4.1.12356.101.12.2.2.1"

	// License contracts — indexed by fgVdEntIndex (per VDOM).
	FgLicContractEntry = "1.3.6.1.4.1.12356.101.1.6.3.1.2.1"
)

// fgHaStatsEntry column numbers (single fgHaStatsIndex index).
const (
	FgHaStatsColSerial   = 2
	FgHaStatsColCpu      = 3
	FgHaStatsColMem      = 4
	FgHaStatsColSesCount = 6
	FgHaStatsColHostname = 11
	FgHaStatsColSyncStat = 12
)

// fgVpnTunEntry column numbers. In/Out octets are Counter64.
const (
	FgVpnTunColP1Name = 2
	FgVpnTunColP2Name = 3
	FgVpnTunColRemGwy = 4
	FgVpnTunColInOct  = 18 // Counter64
	FgVpnTunColOutOct = 19 // Counter64
	FgVpnTunColStatus = 20 // 1=down 2=up
)

// fgLicContractEntry column numbers.
const (
	FgLicColDesc   = 1
	FgLicColExpiry = 2
)

// Enum values.
const (
	FgHaModeStandalone    = 1
	FgHaModeActiveActive  = 2
	FgHaModeActivePassive = 3
	FgVpnStatusDown       = 1
	FgVpnStatusUp         = 2
	FgHaSyncUnsync        = 0
	FgHaSyncSync          = 1
)
