// Package mibs holds SNMP OID constants used across HIMS drivers and
// the topology engine. All values are from published MIBs; the live
// device is always the source of truth for column indexes.
package mibs

const (
	// --- SNMPv2-MIB (system) -----------------------------------------------
	SysDescr    = "1.3.6.1.2.1.1.1.0"
	SysObjectID = "1.3.6.1.2.1.1.2.0"
	SysUpTime   = "1.3.6.1.2.1.1.3.0"
	SysName     = "1.3.6.1.2.1.1.5.0"

	// --- IF-MIB (interfaces) -----------------------------------------------
	IfEntry  = "1.3.6.1.2.1.2.2.1"              // ifTable entry root
	IfXEntry = "1.3.6.1.4.1.5.1.31.1.1.1.1.1.1" // placeholder; real ifXEntry below

	// IF-MIB column numbers within ifEntry (column 1 = ifIndex, excluded from ColumnAndIndex)
	IfDescr       = 2
	IfType        = 3
	IfPhysAddress = 6 // MAC
	IfAdminStatus = 7
	IfOperStatus  = 8

	// ifXTable (RFC 2863)
	IfXEntry1       = "1.3.6.1.2.1.31.1.1.1" // ifXTable entry root
	IfXColName      = 1                      // ifName
	IfXColAlias     = 18                     // ifAlias
	IfXColHighSpeed = 15                     // ifHighSpeed (Mbps)

	// --- Q-BRIDGE-MIB (VLANs) ---------------------------------------------
	Dot1qVlanStaticEntry   = "1.3.6.1.2.1.17.7.1.4.3.1" // vlan_id → name/status
	Dot1qVlanCurrentEntry  = "1.3.6.1.2.1.17.7.1.4.2.1" // vlan_id → egress/untagged ports bitmap
	Dot1qPortVlanEntry     = "1.3.6.1.2.1.17.7.1.4.5.1" // port_num → pvid
	Dot1qTpFdbEntry        = "1.3.6.1.2.1.17.7.1.2.2.1" // (vlan_id, mac) → port, status
	Dot1qVlanStaticColName = 1                          // dot1qVlanStaticName
	Dot1qTpFdbColPort      = 2                          // dot1qTpFdbPort
	Dot1qTpFdbColStatus    = 3                          // dot1qTpFdbStatus
	Dot1dTpFdbEntry        = "1.3.6.1.2.1.17.4.3.1"     // legacy bridge FDB (VLAN=0)
	Dot1dTpFdbColAddr      = 1
	Dot1dTpFdbColPort      = 2
	Dot1dTpFdbColStatus    = 3

	// --- LLDP-MIB ----------------------------------------------------------
	LldpLocPortEntry           = "1.0.8802.1.1.2.1.3.7.1" // local port table
	LldpRemEntry               = "1.0.8802.1.1.2.1.4.1.1" // remote table (composite index)
	LldpRemColChassisIDSubtype = 4
	LldpRemColChassisID        = 5
	LldpRemColPortIDSubtype    = 6
	LldpRemColPortID           = 7
	LldpRemColPortDesc         = 8
	LldpRemColSysName          = 9
	LldpRemColSysDesc          = 10
	LldpRemColMgmtAddrEntry    = "1.0.8802.1.1.2.1.4.2.1" // management address sub-table

	// HP ProCurve / Aruba enterprise OID prefixes
	HPEnterprise    = "1.3.6.1.4.1.11."
	ArubaEnterprise = "1.3.6.1.4.1.14823."
	ArubaOSCX       = "1.3.6.1.4.1.47196."

	// Cisco + Huawei enterprise OID prefixes
	CiscoEnterprise  = "1.3.6.1.4.1.9."
	HuaweiEnterprise = "1.3.6.1.4.1.2011."

	// --- CISCO-CDP-MIB (cdpCacheTable) ------------------------------------
	// Index is (cdpCacheIfIndex, cdpCacheDeviceIndex) — 2 elements.
	CdpCacheEntry         = "1.3.6.1.4.1.9.9.23.1.2.1.1"
	CdpCacheColAddress    = 4 // cdpCacheAddress (remote mgmt addr, often 4-byte)
	CdpCacheColDeviceID   = 6 // cdpCacheDeviceId (remote sysName)
	CdpCacheColDevicePort = 7 // cdpCacheDevicePort (remote port)
	CdpCacheColPlatform   = 8 // cdpCachePlatform (remote model)

	// Server enterprise OID prefixes
	NetSnmpEnterprise   = "1.3.6.1.4.1.8072." // net-snmp (Linux/BSD agents)
	MicrosoftEnterprise = "1.3.6.1.4.1.311."  // Windows SNMP service

	// Virtualization / hardware-vendor enterprise OID prefixes
	VMwareEnterprise = "1.3.6.1.4.1.6876." // VMware ESXi host SNMP agent
	HPEServerOID     = "1.3.6.1.4.1.232."  // HP/HPE Insight (iLO host agent)
	DellEnterprise   = "1.3.6.1.4.1.674."  // Dell OpenManage (iDRAC host agent)

	// --- HOST-RESOURCES-MIB ------------------------------------------------
	HrSystemUptime    = "1.3.6.1.2.1.25.1.1.0"
	HrProcessorLoad   = "1.3.6.1.2.1.25.3.3.1.2" // per-CPU load %, walk + average
	HrStorageEntry    = "1.3.6.1.2.1.25.2.3.1"   // hrStorageTable entry root
	HrStorageColType  = 2                        // hrStorageType (OID enum)
	HrStorageColDescr = 3                        // hrStorageDescr
	HrStorageColUnits = 4                        // hrStorageAllocationUnits
	HrStorageColSize  = 5                        // hrStorageSize (in units)
	HrStorageColUsed  = 6                        // hrStorageUsed (in units)
	// hrStorageType values
	HrStorageRAM        = "1.3.6.1.2.1.25.2.1.2"
	HrStorageVirtualMem = "1.3.6.1.2.1.25.2.1.3"
	HrStorageFixedDisk  = "1.3.6.1.2.1.25.2.1.4"
)
