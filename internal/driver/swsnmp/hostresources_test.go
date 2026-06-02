package swsnmp

import (
	"context"
	"testing"

	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// fakeClient is a minimal in-memory SNMP double for swsnmp tests.
type fakeClient struct {
	gets  map[string]snmp.PDU
	walks map[string][]snmp.PDU
}

func (f *fakeClient) Connect(context.Context) error { return nil }
func (f *fakeClient) Close() error                  { return nil }
func (f *fakeClient) Get(_ context.Context, oids ...string) ([]snmp.PDU, error) {
	out := make([]snmp.PDU, len(oids))
	for i, o := range oids {
		if p, ok := f.gets[o]; ok {
			out[i] = p
			out[i].OID = o
		} else {
			out[i] = snmp.PDU{OID: o, Type: snmp.TypeNoSuchInstance}
		}
	}
	return out, nil
}
func (f *fakeClient) Walk(ctx context.Context, root string, fn snmp.WalkFunc) error {
	return f.BulkWalk(ctx, root, fn)
}
func (f *fakeClient) BulkWalk(_ context.Context, root string, fn snmp.WalkFunc) error {
	for key, pdus := range f.walks {
		if !snmp.HasOIDPrefix(key, root) && key != root {
			continue
		}
		for _, p := range pdus {
			if err := fn(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func gi(oid string, v int) snmp.PDU     { return snmp.PDU{OID: oid, Type: snmp.TypeInt, Value: v} }
func gauge(oid string, v uint) snmp.PDU { return snmp.PDU{OID: oid, Type: snmp.TypeGauge32, Value: v} }
func oct(oid, v string) snmp.PDU {
	return snmp.PDU{OID: oid, Type: snmp.TypeOctetString, Value: []byte(v)}
}
func oidv(oid, v string) snmp.PDU { return snmp.PDU{OID: oid, Type: snmp.TypeOIDValue, Value: v} }

func TestCollectHostResources_CPUAndStorage(t *testing.T) {
	e := mibs.HrStorageEntry
	fc := &fakeClient{
		gets: map[string]snmp.PDU{
			mibs.HrSystemUptime: gauge(mibs.HrSystemUptime, 123456),
		},
		walks: map[string][]snmp.PDU{
			// Two processors at 10% and 30% → average 20.
			mibs.HrProcessorLoad: {
				gi(mibs.HrProcessorLoad+".1", 10),
				gi(mibs.HrProcessorLoad+".2", 30),
			},
			// hrStorageTable: index 1 = RAM (8 GiB, 4 GiB used),
			// index 2 = fixed disk (100 GiB, 60 GiB used). units = 1024.
			e: {
				oidv(e+".2.1", mibs.HrStorageRAM),
				oct(e+".3.1", "Physical memory"),
				gi(e+".4.1", 1024),
				gi(e+".5.1", 8388608), // 8 GiB / 1024 = 8388608 units
				gi(e+".6.1", 4194304), // 4 GiB used
				oidv(e+".2.2", mibs.HrStorageFixedDisk),
				oct(e+".3.2", "/"),
				gi(e+".4.2", 1024),
				gi(e+".5.2", 104857600), // 100 GiB
				gi(e+".6.2", 62914560),  // 60 GiB
			},
		},
	}
	hr := CollectHostResources(context.Background(), fc)
	if hr.CPULoadPct != 20 {
		t.Errorf("CPU load avg = %d, want 20", hr.CPULoadPct)
	}
	if hr.UptimeCS != 123456 {
		t.Errorf("uptime = %d, want 123456", hr.UptimeCS)
	}
	if len(hr.Storage) != 2 {
		t.Fatalf("want 2 storage rows, got %d", len(hr.Storage))
	}
	var ram, disk bool
	for _, s := range hr.Storage {
		switch s.Type {
		case "ram":
			ram = true
			if s.TotalBytes != 8388608*1024 {
				t.Errorf("RAM total = %d, want %d", s.TotalBytes, int64(8388608)*1024)
			}
		case "disk":
			disk = true
			if s.Descr != "/" {
				t.Errorf("disk descr = %q, want /", s.Descr)
			}
		}
	}
	if !ram || !disk {
		t.Errorf("expected both ram + disk rows, ram=%v disk=%v", ram, disk)
	}
}
