package hyperv

import (
	"context"
	"testing"

	"github.com/coralsearesorts/hims/internal/driver"
)

type fakeRunner struct{ out string }

func (f fakeRunner) Run(context.Context, string) (string, error) { return f.out, nil }

func TestFingerprint_AlwaysNoMatch(t *testing.T) {
	d := New()
	// Even a Windows + WinRM probe must not auto-classify as a hypervisor.
	m := d.Fingerprint(driver.Probe{HTTPServer: "Microsoft-HTTPAPI", OpenTCPPorts: []int{5985}})
	if m.Confidence != 0 {
		t.Fatalf("hyperv fingerprint must be NoMatch; got %+v", m)
	}
}

func TestCollect_MapsVMs(t *testing.T) {
	d := New()
	out := `[{"Name":"vm1","State":2,"ProcessorCount":2,"MemoryMB":4096}]`
	f, err := d.Collect(&Session{Runner: fakeRunner{out: out}, Ctx: context.Background()}, driver.Probe{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.VMs) != 1 || f.VMs[0].Name != "vm1" || f.VMs[0].PowerState != "on" {
		t.Fatalf("VM not mapped: %+v", f.VMs)
	}
	if f.KV["hypervisor.type"] != "hyperv" {
		t.Fatal("hypervisor.type fact missing")
	}
}

func TestCollect_WrongSession(t *testing.T) {
	if _, err := New().Collect(&driver.SessionBase{}, driver.Probe{}); err == nil {
		t.Fatal("expected error for non-hyperv session")
	}
}
