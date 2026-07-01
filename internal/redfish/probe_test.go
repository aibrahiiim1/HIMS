package redfish

import (
	"context"
	"testing"
)

// The real Dell iDRAC9 ServiceRoot payload (captured unauthenticated from a live
// iDRAC): Vendor=Dell, Product=iDRAC, Oem.Dell.ServiceTag is the chassis serial.
const dellServiceRoot = `{
  "@odata.id":"/redfish/v1","Id":"RootService","Name":"Root Service",
  "Product":"Integrated Dell Remote Access Controller","Vendor":"Dell",
  "RedfishVersion":"1.17.0",
  "Oem":{"Dell":{"@odata.type":"#DellServiceRoot.v1_0_0.DellServiceRoot",
    "ManagerMACAddress":"b8:cb:29:de:e8:bf","ServiceTag":"60T5XM3","IsBranded":0}},
  "Managers":{"@odata.id":"/redfish/v1/Managers"},
  "Systems":{"@odata.id":"/redfish/v1/Systems"},
  "Chassis":{"@odata.id":"/redfish/v1/Chassis"}}`

func TestProbeServiceRoot_DellIDRAC(t *testing.T) {
	doer := fakeDoer{routes: map[string]string{"/redfish/v1/": dellServiceRoot}}
	info := ProbeServiceRoot(context.Background(), "https://172.21.96.112", doer)
	if !info.Reachable {
		t.Fatal("expected Reachable=true for a valid Dell ServiceRoot")
	}
	if info.Vendor != "Dell" {
		t.Errorf("Vendor = %q, want Dell", info.Vendor)
	}
	if info.ControllerKind != "iDRAC" {
		t.Errorf("ControllerKind = %q, want iDRAC", info.ControllerKind)
	}
	if info.ServiceTag != "60T5XM3" {
		t.Errorf("ServiceTag = %q, want 60T5XM3 (the real chassis serial)", info.ServiceTag)
	}
	if info.RedfishVersion != "1.17.0" {
		t.Errorf("RedfishVersion = %q, want 1.17.0", info.RedfishVersion)
	}
}

func TestProbeServiceRoot_HPEiLO(t *testing.T) {
	root := `{"Vendor":"HPE","Product":"ProLiant","RedfishVersion":"1.6.0","Oem":{"Hpe":{}}}`
	doer := fakeDoer{routes: map[string]string{"/redfish/v1/": root}}
	info := ProbeServiceRoot(context.Background(), "https://10.0.0.50", doer)
	if !info.Reachable || info.Vendor != "HPE" || info.ControllerKind != "iLO" {
		t.Errorf("HPE iLO: got Reachable=%v Vendor=%q Kind=%q", info.Reachable, info.Vendor, info.ControllerKind)
	}
	if info.ServiceTag != "" {
		t.Errorf("HPE has no Dell ServiceTag, got %q", info.ServiceTag)
	}
}

func TestProbeServiceRoot_GenericRedfishNoOem(t *testing.T) {
	// A Redfish BMC of an unrecognized vendor — still honestly detected as a controller.
	root := `{"Product":"BMC","RedfishVersion":"1.0.0"}`
	doer := fakeDoer{routes: map[string]string{"/redfish/v1/": root}}
	info := ProbeServiceRoot(context.Background(), "https://10.0.0.60", doer)
	if !info.Reachable || info.ControllerKind != "redfish" {
		t.Errorf("generic Redfish: got Reachable=%v Kind=%q", info.Reachable, info.ControllerKind)
	}
}

func TestProbeServiceRoot_NotRedfish(t *testing.T) {
	// No /redfish/v1/ route → 404 → not reachable, no fabricated identity.
	doer := fakeDoer{routes: map[string]string{}}
	info := ProbeServiceRoot(context.Background(), "https://10.0.0.99", doer)
	if info.Reachable || info.Vendor != "" || info.ControllerKind != "" {
		t.Errorf("non-Redfish host must yield empty info, got %+v", info)
	}
}
