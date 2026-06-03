package hyperv

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) Run(context.Context, string) (string, error) { return f.out, f.err }

func TestCollectVMs_Array(t *testing.T) {
	// Many VMs → ConvertTo-Json emits an array; State as numeric enum.
	out := `[{"Name":"web01","State":2,"ProcessorCount":4,"MemoryMB":8192,"Guest":"OkApplicationsHealthy"},
	         {"Name":"db01","State":3,"ProcessorCount":8,"MemoryMB":32768,"Guest":"NoContact"}]`
	vms, err := CollectVMs(context.Background(), fakeRunner{out: out})
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 2 {
		t.Fatalf("got %d VMs; want 2", len(vms))
	}
	if vms[0].Name != "web01" || vms[0].PowerState != "on" || vms[0].VCPU != 4 || vms[0].MemoryMB != 8192 {
		t.Fatalf("web01 wrong: %+v", vms[0])
	}
	if vms[1].PowerState != "off" {
		t.Fatalf("db01 state = %q; want off", vms[1].PowerState)
	}
}

func TestCollectVMs_SingleObject(t *testing.T) {
	// One VM → ConvertTo-Json emits a bare object; State as string.
	out := `{"Name":"solo","State":"Running","ProcessorCount":2,"MemoryMB":4096}`
	vms, err := CollectVMs(context.Background(), fakeRunner{out: out})
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 1 || vms[0].Name != "solo" || vms[0].PowerState != "on" {
		t.Fatalf("single-object parse wrong: %+v", vms)
	}
}

func TestCollectVMs_SuspendedAndUnknown(t *testing.T) {
	out := `[{"Name":"a","State":9},{"Name":"b","State":42}]`
	vms, _ := CollectVMs(context.Background(), fakeRunner{out: out})
	if vms[0].PowerState != "suspended" || vms[1].PowerState != "unknown" {
		t.Fatalf("state mapping wrong: %+v", vms)
	}
}

func TestCollectVMs_Empty(t *testing.T) {
	vms, err := CollectVMs(context.Background(), fakeRunner{out: "  "})
	if err != nil || len(vms) != 0 {
		t.Fatalf("empty output should yield no VMs, no error; got %v / %v", vms, err)
	}
}

func TestCollectVMs_RunnerError(t *testing.T) {
	if _, err := CollectVMs(context.Background(), fakeRunner{err: errors.New("winrm down")}); err == nil {
		t.Fatal("runner error should propagate")
	}
}
