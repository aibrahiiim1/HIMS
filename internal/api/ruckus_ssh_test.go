package api

import (
	"strings"
	"testing"
)

// Fixtures are verbatim excerpts captured live from the ZoneDirector 3000 CLI.

const zdSysinfoFixture = `System Overview:
  Name= CSHV-ZD
  IP Address= 192.168.2.2
  MAC Address= C0:C5:20:3D:3E:E0
  Uptime= 41d 20h 18m
  Model= ZD3050
  Licensed APs= 500
  Serial Number= 431408000005
  Version= 10.1.2.0 build 306
Devices Overview:
  Number of APs= 201
  Number of Client Devices= 725
  Number of Rogue Devices= 0`

const zdApFixture = `AP:
  ID:
    1:
      MAC Address= 84:18:3a:10:b4:70
      Model= zf7372
      Device Name= office
      Network Setting:
        IP Address= 192.168.2.143
      Mesh:
        Status= Enabled
      Uplink:
        Status= Smart
    2:
      MAC Address= 84:18:3a:20:91:40
      Model= zf7372
      Device Name= lobby
      Mesh:
        Status= Disabled`

const zdClientFixture = `Current Active Clients:
  Clients:
    Mac Address= 2e:ac:7f:e4:98:00
    User/IP= 172.21.4.157
    Access Point= f0:b0:52:37:f6:80
    WLAN= Coral Sea WiFi
    Signal= 25
    Status= Authorized
  Clients:
    Mac Address= 16:19:6c:a7:29:14
    User/IP= 172.21.4.158
    WLAN= chr`

const zdWlanFixture = `WLAN Service:
  ID:
    1:
      NAME = chr
      SSID = chr
      Authentication = open
      Encryption = wpa2
      Passphrase = FAKE-TEST-PSK-not-a-real-key
      VLAN-ID = 214
    2:
      NAME = Coral Sea WiFi
      SSID = Coral Sea WiFi
      Authentication = open
      VLAN-ID = 97`

func TestParseZDSysinfo(t *testing.T) {
	id := parseZDSysinfo(zdSysinfoFixture)
	if id.name != "CSHV-ZD" {
		t.Errorf("name = %q, want CSHV-ZD", id.name)
	}
	if id.model != "ZD3050" {
		t.Errorf("model = %q, want ZD3050", id.model)
	}
	if id.serial != "431408000005" {
		t.Errorf("serial = %q", id.serial)
	}
	if id.version != "10.1.2.0 build 306" {
		t.Errorf("version = %q", id.version)
	}
	if id.ip != "192.168.2.2" {
		t.Errorf("ip = %q", id.ip)
	}
	if id.apCount != 201 {
		t.Errorf("apCount = %d, want 201", id.apCount)
	}
	if id.clientCount != 725 {
		t.Errorf("clientCount = %d, want 725", id.clientCount)
	}
}

func TestZDCountIndented(t *testing.T) {
	// AP blocks keyed by "MAC Address=" (note casing differs from clients).
	if n := zdCountIndented(zdApFixture, "MAC Address="); n != 2 {
		t.Errorf("AP count = %d, want 2", n)
	}
	// Clients keyed by "Mac Address=".
	if n := zdCountIndented(zdClientFixture, "Mac Address="); n != 2 {
		t.Errorf("client count = %d, want 2", n)
	}
	// WLANs keyed by "NAME =".
	if n := zdCountIndented(zdWlanFixture, "NAME ="); n != 2 {
		t.Errorf("WLAN count = %d, want 2", n)
	}
	// AP "MAC Address=" must NOT match client "Mac Address=" (case-sensitive).
	if n := zdCountIndented(zdClientFixture, "MAC Address="); n != 0 {
		t.Errorf("client block matched AP key: %d, want 0", n)
	}
}

// TestZDPassphraseRedaction guards that a WLAN PSK is never left in a stored
// preview (the reZDPassphrase scrub runs before persistence).
func TestZDPassphraseRedaction(t *testing.T) {
	scrubbed := reZDPassphrase.ReplaceAllString(zdWlanFixture, "${1}${2}***")
	if strings.Contains(scrubbed, "FAKE-TEST-PSK-not-a-real-key") {
		t.Fatalf("PSK leaked into scrubbed preview:\n%s", scrubbed)
	}
}
