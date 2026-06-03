package snmp

import (
	"encoding/json"
	"strings"

	gs "github.com/gosnmp/gosnmp"
)

// ParseV3JSON decodes an SNMP v3 credential secret (stored as JSON) into USM
// params. Blob shape: {"security_name","auth_protocol","auth_key",
// "priv_protocol","priv_key"}. Shared by discovery + monitoring.
func ParseV3JSON(b []byte) (*V3Params, error) {
	var p struct {
		SecurityName string `json:"security_name"`
		AuthProtocol string `json:"auth_protocol"`
		AuthKey      string `json:"auth_key"`
		PrivProtocol string `json:"priv_protocol"`
		PrivKey      string `json:"priv_key"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &V3Params{
		SecurityName: p.SecurityName, AuthProtocol: p.AuthProtocol, AuthKey: p.AuthKey,
		PrivProtocol: p.PrivProtocol, PrivKey: p.PrivKey,
	}, nil
}

// toV3 builds the gosnmp v3 MsgFlags + USM security parameters from V3Params.
// The security level is derived from which keys are present: both auth+priv →
// AuthPriv; auth only → AuthNoPriv; neither → NoAuthNoPriv. Unknown protocol
// strings fall back to a safe default (SHA / AES) rather than erroring, since
// the device negotiates and a wrong guess simply fails to authenticate.
func toV3(p *V3Params) (gs.SnmpV3MsgFlags, *gs.UsmSecurityParameters) {
	usm := &gs.UsmSecurityParameters{UserName: p.SecurityName}

	hasAuth := p.AuthProtocol != "" && p.AuthKey != ""
	hasPriv := hasAuth && p.PrivProtocol != "" && p.PrivKey != ""

	flags := gs.NoAuthNoPriv
	if hasAuth {
		flags = gs.AuthNoPriv
		usm.AuthenticationProtocol = authProto(p.AuthProtocol)
		usm.AuthenticationPassphrase = p.AuthKey
	}
	if hasPriv {
		flags = gs.AuthPriv
		usm.PrivacyProtocol = privProto(p.PrivProtocol)
		usm.PrivacyPassphrase = p.PrivKey
	}
	return flags, usm
}

// authProto maps a name to a gosnmp auth protocol (default SHA).
func authProto(s string) gs.SnmpV3AuthProtocol {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "MD5":
		return gs.MD5
	case "SHA", "SHA1":
		return gs.SHA
	case "SHA224":
		return gs.SHA224
	case "SHA256":
		return gs.SHA256
	case "SHA384":
		return gs.SHA384
	case "SHA512":
		return gs.SHA512
	default:
		return gs.SHA
	}
}

// privProto maps a name to a gosnmp privacy protocol (default AES).
func privProto(s string) gs.SnmpV3PrivProtocol {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DES":
		return gs.DES
	case "AES", "AES128":
		return gs.AES
	case "AES192":
		return gs.AES192
	case "AES256":
		return gs.AES256
	default:
		return gs.AES
	}
}

// SecurityLevel reports the negotiated level for V3Params, for logging/UI.
// Returns "noAuthNoPriv" | "authNoPriv" | "authPriv".
func (p *V3Params) SecurityLevel() string {
	if p == nil || p.AuthProtocol == "" || p.AuthKey == "" {
		return "noAuthNoPriv"
	}
	if p.PrivProtocol == "" || p.PrivKey == "" {
		return "authNoPriv"
	}
	return "authPriv"
}
