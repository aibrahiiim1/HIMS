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
	CatUnknown            DeviceCategory = "unknown"
	CatSwitch             DeviceCategory = "switch"
	CatRouter             DeviceCategory = "router"
	CatFirewall           DeviceCategory = "firewall"
	CatAccessPoint        DeviceCategory = "access_point"
	CatWirelessController DeviceCategory = "wireless_controller"
	CatServer             DeviceCategory = "server"
	CatVirtualHost        DeviceCategory = "virtual_host"
	CatVirtualMachine     DeviceCategory = "virtual_machine"
	CatStorage            DeviceCategory = "storage"
	CatNVR                DeviceCategory = "nvr"
	CatCamera             DeviceCategory = "camera"
	CatPrinter            DeviceCategory = "printer"
	CatIPPhone            DeviceCategory = "ip_phone"
	CatPBX                DeviceCategory = "pbx"
	CatVoiceGateway       DeviceCategory = "voice_gateway"
	CatDatabase           DeviceCategory = "database"
	CatDirectory          DeviceCategory = "directory"
	CatDNS                DeviceCategory = "dns"
	CatDHCP               DeviceCategory = "dhcp"
	CatFingerprint        DeviceCategory = "fingerprint"
	CatEndpoint           DeviceCategory = "endpoint"
	CatUPS                DeviceCategory = "ups"
	CatISPRouter          DeviceCategory = "isp_router"
	CatApplication        DeviceCategory = "application"
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
