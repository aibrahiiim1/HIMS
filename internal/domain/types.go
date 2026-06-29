// Package domain holds HIMS's core entities and repository interfaces. It
// has no infrastructure dependencies (no pgx, no SNMP) so it can be reused
// by the API, the collector, and tests without dragging in transports.
package domain

import (
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// LocationKind is a node type in the Hotel Group → … → Rack tree.
type LocationKind string

const (
	LocationGroup    LocationKind = "group"
	LocationHotel    LocationKind = "hotel"
	LocationBuilding LocationKind = "building"
	LocationFloor    LocationKind = "floor"
	LocationArea     LocationKind = "area"
	LocationRoom     LocationKind = "room"
	LocationRack     LocationKind = "rack"
)

// Location is one node of the location tree.
type Location struct {
	ID        uuid.UUID
	ParentID  *uuid.UUID
	Kind      LocationKind
	Name      string
	Code      *string
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Subnet is the unit of discovery + credential scope, pinned to a location.
type Subnet struct {
	ID         uuid.UUID
	LocationID uuid.UUID
	CIDR       netip.Prefix
	Name       *string
	VLANID     *int32
	Metadata   map[string]any
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CredentialKind is the protocol family a credential authenticates.
type CredentialKind string

const (
	CredSNMPv2c   CredentialKind = "snmp_v2c"
	CredSNMPv3    CredentialKind = "snmp_v3"
	CredSSH       CredentialKind = "ssh"
	CredWinRM     CredentialKind = "winrm"
	CredHTTPBasic CredentialKind = "http_basic"
	CredONVIF     CredentialKind = "onvif"
	CredVendorAPI CredentialKind = "vendor_api"
	CredLDAP      CredentialKind = "ldap"
	CredWMI       CredentialKind = "wmi"     // Windows WMI/DCOM (legacy Windows fallback)
	CredWindows   CredentialKind = "windows" // unified Windows login — system uses WinRM, WMI/DCOM, or the relay agent as needed
	CredZKTeco    CredentialKind = "zkteco"  // ZKTeco device communication key (sealed; native TCP/4370 protocol)
	CredCLI       CredentialKind = "cli"     // generic CLI login (user:password) for telnet-only devices, e.g. Alcatel OmniPCX mtcl
)

// Credential is an encrypted secret. EncryptedBlob is never logged or
// returned over the API; only metadata (name/kind/weak) is operator-facing.
type Credential struct {
	ID            uuid.UUID
	Name          string
	Kind          CredentialKind
	EncryptedBlob []byte
	KeyID         string
	Weak          bool
	Metadata      map[string]any
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CredentialGroup is a named bundle of credentials bound to scopes.
type CredentialGroup struct {
	ID          uuid.UUID
	Name        string
	Description *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DeviceCategory drives which detail template renders.
type DeviceCategory string

const (
	CatUnknown DeviceCategory = "unknown"
	// CatNetworkUnclassified: the device answered SNMP (it IS a managed network device)
	// but no fingerprint/keyword pinned its exact type. An HONEST label distinct from a
	// bare "unknown" (which means no signal at all) — so an SNMP-managed device is never
	// reported as a vague unknown. A vendor fingerprint for its sysObjectID promotes it.
	CatNetworkUnclassified DeviceCategory = "network_device_unclassified"
	CatSwitch              DeviceCategory = "switch"
	CatRouter              DeviceCategory = "router"
	CatFirewall            DeviceCategory = "firewall"
	CatAccessPoint         DeviceCategory = "access_point"
	CatWirelessController  DeviceCategory = "wireless_controller"
	CatServer              DeviceCategory = "server"
	CatVirtualHost         DeviceCategory = "virtual_host"
	CatVirtualMachine      DeviceCategory = "virtual_machine"
	CatStorage             DeviceCategory = "storage"
	CatNVR                 DeviceCategory = "nvr"
	CatDVR                 DeviceCategory = "dvr"
	CatCamera              DeviceCategory = "camera"
	CatPrinter             DeviceCategory = "printer"
	CatIPPhone             DeviceCategory = "ip_phone"
	CatPBX                 DeviceCategory = "pbx"
	CatVoiceGateway        DeviceCategory = "voice_gateway"
	CatDatabase            DeviceCategory = "database"
	CatDirectory           DeviceCategory = "directory"
	CatDNS                 DeviceCategory = "dns"
	CatDHCP                DeviceCategory = "dhcp"
	CatFingerprint         DeviceCategory = "fingerprint"
	CatEndpoint            DeviceCategory = "endpoint"
	CatUPS                 DeviceCategory = "ups"
	CatISPRouter           DeviceCategory = "isp_router"
	CatApplication         DeviceCategory = "application"
	CatLoadBalancer        DeviceCategory = "load_balancer" // F5/Citrix ADC/A10/Kemp
	CatPDU                 DeviceCategory = "pdu"           // switched/metered rack PDU (distinct from UPS)
	// Out-of-band management controllers — a SEPARATE device from the server they manage
	// (own IP), so they belong in their own inventory view, not mixed with servers.
	CatBMC DeviceCategory = "bmc" // HPE iLO / Dell iDRAC / Lenovo XClarity-IMM / Supermicro IPMI / Redfish
	// Biometric / time-attendance / access-control devices.
	CatBiometric             DeviceCategory = "biometric"
	CatBiometricUnclassified DeviceCategory = "biometric_device_unclassified" // evidence=biometric, exact type unknown
	// Point-of-sale terminals / POS PCs / payment endpoints.
	CatPOS             DeviceCategory = "pos"
	CatPOSUnclassified DeviceCategory = "pos_device_unclassified" // evidence=POS, exact type unknown
)

// IsStickyInfraCategory reports whether a category is managed network/wireless
// infrastructure whose established identity must NOT be downgraded by a weak,
// unauthenticated re-scan — e.g. a transient SNMP timeout that leaves only an
// SSH-banner / open-port guess ("server"). A scan may still RECLASSIFY such a
// device, but only on AUTHORITATIVE evidence (SNMP identity answered, or a
// driver/fingerprint match). See apply.reconcile, which uses this to keep a
// known wireless controller from flipping to "server" on a single SNMP timeout.
func IsStickyInfraCategory(cat string) bool {
	switch DeviceCategory(cat) {
	case CatWirelessController, CatAccessPoint, CatSwitch, CatRouter, CatFirewall, CatISPRouter, CatLoadBalancer:
		return true
	}
	return false
}

// DeviceRole is a role a device fulfils; a device may hold several at once
// (e.g. a Windows box that is DC + DNS + DHCP). Mirrors the device_roles
// table's CHECK set.
type DeviceRole string

const (
	RoleHyperVHost       DeviceRole = "hyperv_host"
	RoleESXiHost         DeviceRole = "esxi_host"
	RoleDomainController DeviceRole = "domain_controller"
	RoleDNS              DeviceRole = "dns"
	RoleDHCP             DeviceRole = "dhcp"
	RoleSQLServer        DeviceRole = "sql_server"
	RoleOracle           DeviceRole = "oracle"
	RolePostgreSQL       DeviceRole = "postgresql"
	RoleFileServer       DeviceRole = "file_server"
	RoleWebServer        DeviceRole = "web_server"
	RoleWirelessControl  DeviceRole = "wireless_controller"
	RoleVoice            DeviceRole = "voice"
	RoleRouter           DeviceRole = "router"
	RoleFirewall         DeviceRole = "firewall"
)

// DeviceStatus is the reachability/health rollup.
type DeviceStatus string

const (
	StatusUp      DeviceStatus = "up"
	StatusDown    DeviceStatus = "down"
	StatusWarning DeviceStatus = "warning"
	StatusUnknown DeviceStatus = "unknown"
)

// Device is the generic CMDB base. Vendor specifics live in DeviceFact, not
// here (ADR 0001).
type Device struct {
	ID               uuid.UUID
	LocationID       *uuid.UUID
	PrimaryIP        *netip.Addr
	Hostname         *string
	Name             string
	Vendor           *string
	Model            *string
	Serial           *string
	OSVersion        *string
	Category         DeviceCategory
	Status           DeviceStatus
	Driver           *string
	CredentialID     *uuid.UUID
	LastDiscoveryAt  *time.Time
	LastMonitoringAt *time.Time
	Metadata         map[string]any
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// DeviceFact is a normalized, per-driver fact about a device.
type DeviceFact struct {
	DeviceID   uuid.UUID
	Key        string
	Value      *string
	ValueJSON  map[string]any
	Driver     string
	ObservedAt time.Time
}
