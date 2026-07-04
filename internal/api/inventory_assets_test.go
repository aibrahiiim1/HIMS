package api

import (
	"testing"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// TestClassifyAssets folds SVI + linked-BMC endpoints into their parent asset using
// evidence only, keeps weak (hostname) links out of the fold as review items, and never
// merges by guess. This is the canonical-inventory regression guard.
func TestClassifyAssets(t *testing.T) {
	sw := uuid.New()
	svID := uuid.New()  // SVI gateway IP device (folds into switch)
	srvID := uuid.New() // physical server (serial CZ1)
	bmcOK := uuid.New() // BMC whose serial == CZ1  -> folds into server
	bmcHN := uuid.New() // BMC linked only by hostname stem -> review, NOT folded
	bmcNo := uuid.New() // BMC with no match -> its own physical-server asset
	printer := uuid.New()

	server := db.Device{ID: srvID, Category: "server", Serial: strp("CZ1"), Hostname: strp("srv01")}
	devs := []db.Device{
		{ID: sw, Category: "switch", Name: "CHV-CORE"},
		{ID: svID, Category: "switch", Name: "172.21.210.250"},
		server,
		{ID: bmcOK, Category: "bmc", Serial: strp("CZ1"), Hostname: strp("srv01-ilo")},
		{ID: bmcHN, Category: "bmc", Serial: strp(""), Hostname: strp("srv01-ilo")},
		{ID: bmcNo, Category: "bmc", Serial: strp("ZZ9"), Hostname: strp("lonely-ilo")},
		{ID: printer, Category: "printer", Name: "LBP"},
	}
	maps := &statusMaps{sviParent: map[uuid.UUID]sviRef{
		svID: {Switch: hostRef{ID: sw, Name: "CHV-CORE"}, VLAN: "210"},
	}}
	idx := serverSerialIndex{
		servers:  []db.Device{server},
		bySerial: map[string]db.Device{"CZ1": server},
		byUUID:   map[string]db.Device{},
	}
	classes, review := classifyAssets(devs, maps, idx, func(db.Device) string { return "" })

	role := map[uuid.UUID]assetRole{}
	parent := map[uuid.UUID]string{}
	for _, c := range classes {
		id, _ := uuid.Parse(c.DeviceID)
		role[id] = c.Role
		parent[id] = c.ParentID
	}
	if role[svID] != roleSVIGateway || parent[svID] != sw.String() {
		t.Errorf("SVI IP must fold into its switch; got role=%s parent=%s", role[svID], parent[svID])
	}
	if role[bmcOK] != roleBMCEndpoint || parent[bmcOK] != srvID.String() {
		t.Errorf("serial-matched BMC must fold into its server; got role=%s parent=%s", role[bmcOK], parent[bmcOK])
	}
	for _, id := range []uuid.UUID{sw, srvID, bmcNo, printer} {
		if role[id] != roleAsset {
			t.Errorf("%s must be a standalone asset; got %s", id, role[id])
		}
	}
	// hostname-only BMC is NOT folded (stays an asset) and IS surfaced for review.
	if role[bmcHN] != roleAsset {
		t.Errorf("hostname-candidate BMC must NOT be auto-folded; got %s", role[bmcHN])
	}
	if len(review) != 1 || review[0].DeviceID != bmcHN.String() {
		t.Fatalf("hostname-candidate BMC must be in needs_review; got %+v", review)
	}

	// Count model: 7 devices - 1 SVI - 1 linked BMC = 5 assets.
	assets := 0
	for _, c := range classes {
		if c.Role == roleAsset {
			assets++
		}
	}
	if assets != 5 {
		t.Errorf("asset count = %d; want 5 (7 devices, SVI + serial-BMC folded)", assets)
	}
}
