package api

import "testing"

func TestManualDeviceParams(t *testing.T) {
	t.Run("name required", func(t *testing.T) {
		if _, err := manualDeviceParams(manualDeviceReq{Name: "  "}); err == nil {
			t.Fatal("blank name should error")
		}
	})
	t.Run("defaults category + status to unknown, source in metadata", func(t *testing.T) {
		p, err := manualDeviceParams(manualDeviceReq{Name: "Patch Panel A"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Category != "unknown" || p.Status != "unknown" {
			t.Fatalf("got category=%q status=%q", p.Category, p.Status)
		}
		if string(p.Metadata) != `{"source":"manual"}` {
			t.Fatalf("metadata = %s", p.Metadata)
		}
		if p.PrimaryIp != nil {
			t.Fatal("no IP should leave PrimaryIp nil (non-discoverable asset)")
		}
	})
	t.Run("valid IP parsed", func(t *testing.T) {
		p, err := manualDeviceParams(manualDeviceReq{Name: "SW1", Category: "switch", PrimaryIP: "10.0.0.9"})
		if err != nil {
			t.Fatal(err)
		}
		if p.PrimaryIp == nil || p.PrimaryIp.String() != "10.0.0.9" {
			t.Fatalf("PrimaryIp = %v", p.PrimaryIp)
		}
	})
	t.Run("bad IP errors", func(t *testing.T) {
		if _, err := manualDeviceParams(manualDeviceReq{Name: "x", PrimaryIP: "not-an-ip"}); err == nil {
			t.Fatal("bad IP should error")
		}
	})
	t.Run("invalid category errors", func(t *testing.T) {
		if _, err := manualDeviceParams(manualDeviceReq{Name: "x", Category: "patch_panel"}); err == nil {
			t.Fatal("unknown category should error with the allowed list")
		}
	})
	t.Run("valid taxonomy category accepted", func(t *testing.T) {
		if _, err := manualDeviceParams(manualDeviceReq{Name: "x", Category: "ups"}); err != nil {
			t.Fatalf("ups is a valid category: %v", err)
		}
	})
}
