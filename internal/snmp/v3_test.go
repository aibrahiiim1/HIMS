package snmp

import (
	"net/netip"
	"testing"

	gs "github.com/gosnmp/gosnmp"
)

func TestToV3_AuthPriv(t *testing.T) {
	flags, usm := toV3(&V3Params{
		SecurityName: "monitor", AuthProtocol: "SHA256", AuthKey: "authpass",
		PrivProtocol: "AES", PrivKey: "privpass",
	})
	if flags != gs.AuthPriv {
		t.Fatalf("flags = %v; want AuthPriv", flags)
	}
	if usm.UserName != "monitor" || usm.AuthenticationProtocol != gs.SHA256 || usm.PrivacyProtocol != gs.AES {
		t.Fatalf("usm wrong: %+v", usm)
	}
	if usm.AuthenticationPassphrase != "authpass" || usm.PrivacyPassphrase != "privpass" {
		t.Fatal("passphrases not set")
	}
}

func TestToV3_AuthNoPriv(t *testing.T) {
	flags, usm := toV3(&V3Params{SecurityName: "u", AuthProtocol: "SHA", AuthKey: "k"})
	if flags != gs.AuthNoPriv {
		t.Fatalf("flags = %v; want AuthNoPriv", flags)
	}
	if usm.PrivacyProtocol != 0 { // unset
		t.Fatalf("priv should be unset, got %v", usm.PrivacyProtocol)
	}
}

func TestToV3_NoAuthNoPriv(t *testing.T) {
	flags, _ := toV3(&V3Params{SecurityName: "u"})
	if flags != gs.NoAuthNoPriv {
		t.Fatalf("flags = %v; want NoAuthNoPriv", flags)
	}
}

func TestToV3_PrivRequiresAuth(t *testing.T) {
	// priv keys without auth must NOT yield AuthPriv (USM forbids privNoAuth).
	flags, _ := toV3(&V3Params{SecurityName: "u", PrivProtocol: "AES", PrivKey: "k"})
	if flags != gs.NoAuthNoPriv {
		t.Fatalf("priv-without-auth = %v; want NoAuthNoPriv", flags)
	}
}

func TestProtoMappingDefaults(t *testing.T) {
	if authProto("md5") != gs.MD5 || authProto("SHA512") != gs.SHA512 || authProto("bogus") != gs.SHA {
		t.Fatal("auth proto mapping wrong")
	}
	if privProto("des") != gs.DES || privProto("AES256") != gs.AES256 || privProto("bogus") != gs.AES {
		t.Fatal("priv proto mapping wrong")
	}
}

func TestSecurityLevel(t *testing.T) {
	cases := map[*V3Params]string{
		{SecurityName: "u"}: "noAuthNoPriv",
		{SecurityName: "u", AuthProtocol: "SHA", AuthKey: "k"}:                                    "authNoPriv",
		{SecurityName: "u", AuthProtocol: "SHA", AuthKey: "k", PrivProtocol: "AES", PrivKey: "p"}: "authPriv",
	}
	for p, want := range cases {
		if got := p.SecurityLevel(); got != want {
			t.Errorf("SecurityLevel = %q; want %q", got, want)
		}
	}
}

func TestNewClient_V3RequiresParams(t *testing.T) {
	if _, err := NewClient(Target{Addr: netip.MustParseAddr("10.0.0.1"), Version: V3}); err == nil {
		t.Fatal("v3 target without params should error")
	}
	// With params it constructs.
	c, err := NewClient(Target{
		Addr: netip.MustParseAddr("10.0.0.1"), Version: V3,
		V3: &V3Params{SecurityName: "u", AuthProtocol: "SHA", AuthKey: "k"},
	})
	if err != nil || c == nil {
		t.Fatalf("v3 client with params should build: %v", err)
	}
}
