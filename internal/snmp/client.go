// Package snmp wraps gosnmp with a minimal interface the rest of HIMS
// depends on. The interface (Client) is transport-agnostic so tests
// substitute an in-memory fake without pulling in gosnmp.
package snmp

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	gs "github.com/gosnmp/gosnmp"
)

// Sentinel errors for common failure modes.
var (
	ErrTimeout        = errors.New("snmp: timeout")
	ErrUnreachable    = errors.New("snmp: unreachable")
	ErrBadCredentials = errors.New("snmp: bad credentials")
)

// Version is the SNMP protocol version.
type Version uint8

const (
	V1  Version = 1
	V2c Version = 2
	V3  Version = 3
)

// PDUType mirrors the subset of SNMP BER types HIMS reads.
type PDUType int

const (
	TypeOther PDUType = iota
	TypeInt
	TypeUInt32
	TypeCounter32
	TypeCounter64
	TypeGauge32
	TypeTimeTicks
	TypeOctetString
	TypeOIDValue
	TypeIPAddress
	TypeNoSuchObject
	TypeNoSuchInstance
	TypeEndOfMIBView
)

// PDU is a single OID + value pair, transport-agnostic.
type PDU struct {
	OID   string
	Type  PDUType
	Value any
}

// WalkFunc is the callback for Walk / BulkWalk.
type WalkFunc func(pdu PDU) error

// Target configures an SNMP target.
type Target struct {
	Addr      netip.Addr
	Port      uint16
	Version   Version
	Community string // v1/v2c
	Timeout   time.Duration
	Retries   int
	MaxReps   uint32
	// V3 holds USM parameters; used only when Version == V3.
	V3 *V3Params
}

// V3Params are SNMPv3 USM credentials. The auth/priv protocols are strings
// ("SHA", "SHA256", "MD5", "AES", "AES192", "DES", …) mapped to gosnmp
// constants by toV3 (see v3.go). Empty auth → noAuthNoPriv; auth without priv
// → authNoPriv; both → authPriv.
type V3Params struct {
	SecurityName string
	AuthProtocol string
	AuthKey      string
	PrivProtocol string
	PrivKey      string
}

// WithDefaults fills in sensible zero-value overrides.
func (t Target) WithDefaults() Target {
	if t.Port == 0 {
		t.Port = 161
	}
	if t.Timeout <= 0 {
		t.Timeout = 5 * time.Second
	}
	if t.Retries < 0 {
		t.Retries = 0
	}
	if t.MaxReps == 0 {
		t.MaxReps = 20 //nolint:gomnd
	}
	if t.Version == 0 {
		t.Version = V2c
	}
	return t
}

// Client is the SNMP surface the rest of HIMS uses.
type Client interface {
	Connect(ctx context.Context) error
	Get(ctx context.Context, oids ...string) ([]PDU, error)
	BulkWalk(ctx context.Context, root string, fn WalkFunc) error
	Walk(ctx context.Context, root string, fn WalkFunc) error
	Close() error
}

// concrete gosnmp-backed implementation
type client struct {
	g *gs.GoSNMP
}

// NewClient constructs a Client bound to the target. Call Connect before use.
func NewClient(t Target) (Client, error) {
	t = t.WithDefaults()
	if !t.Addr.IsValid() {
		return nil, errors.New("snmp: target Addr is zero")
	}
	g := &gs.GoSNMP{
		Target:             t.Addr.String(),
		Port:               t.Port,
		Transport:          "udp",
		Community:          t.Community,
		Timeout:            t.Timeout,
		Retries:            t.Retries,
		MaxRepetitions:     t.MaxReps,
		ExponentialTimeout: false,
	}
	switch t.Version {
	case V1:
		g.Version = gs.Version1
	case V3:
		if t.V3 == nil {
			return nil, errors.New("snmp: v3 target requires V3 params")
		}
		g.Version = gs.Version3
		g.SecurityModel = gs.UserSecurityModel
		g.MsgFlags, g.SecurityParameters = toV3(t.V3)
	default:
		g.Version = gs.Version2c
	}
	return &client{g: g}, nil
}

func (c *client) Connect(_ context.Context) error {
	return mapConnectError(c.g.Connect())
}

func (c *client) Close() error { return c.g.Conn.Close() }

func (c *client) Get(_ context.Context, oids ...string) ([]PDU, error) {
	result, err := c.g.Get(oids)
	if err != nil {
		return nil, mapOpError(err)
	}
	out := make([]PDU, 0, len(result.Variables))
	for _, v := range result.Variables {
		out = append(out, fromGoSNMP(v))
	}
	return out, nil
}

func (c *client) BulkWalk(_ context.Context, root string, fn WalkFunc) error {
	return c.g.BulkWalk(root, func(v gs.SnmpPDU) error {
		return fn(fromGoSNMP(v))
	})
}

func (c *client) Walk(_ context.Context, root string, fn WalkFunc) error {
	return c.g.Walk(root, func(v gs.SnmpPDU) error {
		return fn(fromGoSNMP(v))
	})
}

// fromGoSNMP converts a gosnmp PDU to the internal type.
func fromGoSNMP(v gs.SnmpPDU) PDU {
	p := PDU{OID: strings.TrimPrefix(v.Name, ".")}
	switch v.Type {
	case gs.Integer:
		p.Type = TypeInt
	case gs.Uinteger32:
		p.Type = TypeUInt32
	case gs.Counter32:
		p.Type = TypeCounter32
	case gs.Counter64:
		p.Type = TypeCounter64
	case gs.Gauge32:
		p.Type = TypeGauge32
	case gs.TimeTicks:
		p.Type = TypeTimeTicks
	case gs.OctetString:
		p.Type = TypeOctetString
	case gs.ObjectIdentifier:
		p.Type = TypeOIDValue
	case gs.IPAddress:
		p.Type = TypeIPAddress
	case gs.NoSuchObject:
		p.Type = TypeNoSuchObject
	case gs.NoSuchInstance:
		p.Type = TypeNoSuchInstance
	case gs.EndOfMibView:
		p.Type = TypeEndOfMIBView
	default:
		p.Type = TypeOther
	}
	p.Value = v.Value
	return p
}

func mapConnectError(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "timeout"):
		return ErrTimeout
	case strings.Contains(s, "connection refused"), strings.Contains(s, "no route"):
		return ErrUnreachable
	}
	return err
}

func mapOpError(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "timeout"), strings.Contains(s, "request timeout"):
		return ErrTimeout
	case strings.Contains(s, "auth failure"):
		return ErrBadCredentials
	}
	return err
}
