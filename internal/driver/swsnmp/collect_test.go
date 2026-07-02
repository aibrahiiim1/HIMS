package swsnmp

import (
	"reflect"
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
)

func TestDecodePortBitmap(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []int
	}{
		{"empty", []byte{}, []int{}},
		{"msb is port 1", []byte{0x80}, []int{1}},
		{"lsb of byte0 is port 8", []byte{0x01}, []int{8}},
		{"two high bits", []byte{0xC0}, []int{1, 2}},
		{"second byte msb is port 9", []byte{0x00, 0x80}, []int{9}},
		{"all of byte0", []byte{0xFF}, []int{1, 2, 3, 4, 5, 6, 7, 8}},
		{"ports 1 and 10", []byte{0x80, 0x40}, []int{1, 10}},
	}
	for _, c := range cases {
		got := decodePortBitmap(c.in)
		if len(c.want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: decodePortBitmap(%x) = %v; want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestBitSet(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		port int
		want bool
	}{
		{"port 1 set", []byte{0x80}, 1, true},
		{"port 2 unset", []byte{0x80}, 2, false},
		{"port 2 set via 0x40", []byte{0x40}, 2, true},
		{"port 8 set via 0x01", []byte{0x01}, 8, true},
		{"port 9 in 2nd byte", []byte{0x00, 0x80}, 9, true},
		{"out of range", []byte{0x80}, 99, false},
		{"port 0 invalid", []byte{0x80}, 0, false},
		{"empty bitmap", []byte{}, 1, false},
	}
	for _, c := range cases {
		if got := bitSet(c.b, c.port); got != c.want {
			t.Errorf("%s: bitSet(%x, %d) = %v; want %v", c.name, c.b, c.port, got, c.want)
		}
	}
}

func TestDecodeLLDPID(t *testing.T) {
	mac := []byte{0x54, 0x80, 0x28, 0xcb, 0xbc, 0x8a}
	cases := []struct {
		name    string
		subtype int
		raw     []byte
		str     string
		want    string
	}{
		{"mac subtype → MAC hex", 3, mac, "", "54:80:28:cb:bc:8a"},
		{"interfaceName subtype → text", 5, []byte("GigabitEthernet1/0/24"), "", "GigabitEthernet1/0/24"},
		{"local subtype numeric → text", 7, []byte("25"), "", "25"},
		{"networkAddress IPv4", 4, []byte{1, 192, 168, 1, 1}, "", "192.168.1.1"},
		{"non-printable 6 bytes → MAC fallback", 0, mac, "", "54:80:28:cb:bc:8a"},
		{"empty raw uses string fallback", 0, nil, "Gi1", "Gi1"},
		{"trailing nulls trimmed", 5, []byte("Gi3\x00\x00"), "", "Gi3"},
	}
	for _, c := range cases {
		if got := decodeLLDPID(c.subtype, c.raw, c.str); got != c.want {
			t.Errorf("%s: decodeLLDPID(%d,%x,%q) = %q; want %q", c.name, c.subtype, c.raw, c.str, got, c.want)
		}
	}
}

// Tagged/untagged classification: a port present in egress but NOT in the
// untagged set is a tagged (trunk) member; present in both is untagged (access).
func TestEgressUntaggedClassification(t *testing.T) {
	egress := []byte{0xC0} // ports 1, 2
	untag := []byte{0x80}  // port 1 untagged
	if bitSet(untag, 1) != true {
		t.Fatal("port 1 should be untagged (access/native)")
	}
	if bitSet(untag, 2) != false {
		t.Fatal("port 2 should be tagged (trunk member)")
	}
	if !reflect.DeepEqual(decodePortBitmap(egress), []int{1, 2}) {
		t.Fatal("both ports should be VLAN members")
	}
}

// SVI gateway detection: an IP on an interface named "Vlan<id>" (any vendor
// spelling) becomes that VLAN's gateway; physical-port IPs and loopbacks don't
// map; an SVI whose VLAN isn't in the static table is added.
func TestDetectSVIGateways(t *testing.T) {
	ifaces := []driver.InterfaceSnap{
		{IfIndex: 10, IfName: "Vlan210"},              // Cisco/Aruba SVI
		{IfIndex: 11, IfName: "Vlan-interface150"},    // Comware SVI
		{IfIndex: 12, IfName: "irb.96"},               // Juniper SVI
		{IfIndex: 20, IfName: "GigabitEthernet1/0/1"}, // physical port (not SVI)
		{IfIndex: 30, IfName: "loopback0"},            // loopback (no vlan match)
	}
	ips := []driver.IPInterfaceSnap{
		{IP: "172.21.210.250", IfIndex: 10},
		{IP: "172.21.150.1", IfIndex: 11},
		{IP: "172.21.96.2", IfIndex: 12},
		{IP: "10.9.9.1", IfIndex: 20}, // on a physical port -> not a VLAN gateway
		{IP: "1.1.1.1", IfIndex: 30},  // loopback -> ignored
	}
	vlans := []driver.VLANSnap{{VLANID: 210, Name: "GUEST"}, {VLANID: 96, Name: "MGMT"}}
	out := DetectSVIGateways(ifaces, ips, vlans)

	gw := map[int]string{}
	for _, v := range out {
		gw[v.VLANID] = v.GatewayIP
	}
	if gw[210] != "172.21.210.250" {
		t.Errorf("VLAN 210 gateway: got %q want 172.21.210.250", gw[210])
	}
	if gw[96] != "172.21.96.2" {
		t.Errorf("VLAN 96 gateway: got %q want 172.21.96.2", gw[96])
	}
	// VLAN 150 wasn't in the static table but the SVI adds it.
	if gw[150] != "172.21.150.1" {
		t.Errorf("VLAN 150 (SVI-only) gateway: got %q want 172.21.150.1", gw[150])
	}
	// The physical-port IP (10.9.9.1) must not create a phantom VLAN.
	for _, v := range out {
		if v.GatewayIP == "10.9.9.1" || v.GatewayIP == "1.1.1.1" {
			t.Errorf("non-SVI IP wrongly mapped to a VLAN gateway: %+v", v)
		}
	}
}
