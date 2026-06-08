package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// TestResolveClientAPNames covers the live-observed shapes: Ruckus clients
// reference their AP by MAC, Extreme by serial (fallback), and unnamed APs whose
// name equals their MAC must stay unchanged rather than be blanked.
func TestResolveClientAPNames(t *testing.T) {
	aps := []db.AccessPoint{
		{Name: "CSM Meeting Room", Mac: strptr("84:18:3A:10:19:A0"), Serial: "451452800279"},
		{Name: "VillaB", Mac: strptr("f0:b0:52:37:f0:70"), Serial: "471484214131"},
		{Name: "38:ff:36:19:ed:30", Mac: strptr("38:ff:36:19:ed:30"), Serial: "371502003768"}, // unnamed AP
		{Name: "", Mac: strptr("aa:bb:cc:dd:ee:ff"), Serial: "NONAME"},                        // no friendly name → not indexed
	}
	clients := []db.WirelessClient{
		{Mac: "c1", ApName: "84:18:3a:10:19:a0"}, // Ruckus: MAC (case-insensitive) → name
		{Mac: "c2", ApName: "471484214131"},      // Extreme: serial → name
		{Mac: "c3", ApName: "38:ff:36:19:ed:30"}, // unnamed AP → unchanged (name == MAC)
		{Mac: "c4", ApName: "aa:bb:cc:dd:ee:ff"}, // AP has no name → left as the MAC
		{Mac: "c5", ApName: "unknown-ap"},        // no roster match → unchanged
		{Mac: "c6", ApName: ""},                  // empty → unchanged
	}

	resolveClientAPNames(aps, clients)

	want := []string{"CSM Meeting Room", "VillaB", "38:ff:36:19:ed:30", "aa:bb:cc:dd:ee:ff", "unknown-ap", ""}
	for i, w := range want {
		if clients[i].ApName != w {
			t.Errorf("client[%d] (%s): ap_name = %q, want %q", i, clients[i].Mac, clients[i].ApName, w)
		}
	}
}

// TestResolveClientAPNames_NoMutationOnEmpty guards the early returns.
func TestResolveClientAPNames_NoMutationOnEmpty(t *testing.T) {
	resolveClientAPNames(nil, nil) // must not panic
	clients := []db.WirelessClient{{ApName: "x"}}
	resolveClientAPNames(nil, clients) // no APs → no change
	if clients[0].ApName != "x" {
		t.Fatalf("ap_name mutated with empty AP roster: %q", clients[0].ApName)
	}
}
