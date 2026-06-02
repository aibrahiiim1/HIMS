package swsnmp

import (
	"context"
	"strings"

	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/mibs"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// HostResources is the HOST-RESOURCES-MIB collection result.
type HostResources struct {
	UptimeCS   int64
	CPULoadPct int // average across processors
	Storage    []driver.StorageSnap
}

// CollectHostResources reads uptime, average CPU load, and the storage table.
func CollectHostResources(ctx context.Context, c snmp.Client) HostResources {
	var hr HostResources

	if pdus, err := c.Get(ctx, mibs.HrSystemUptime); err == nil && len(pdus) == 1 {
		if v, ok := snmp.PDUInt64(pdus[0]); ok {
			hr.UptimeCS = v
		}
	}

	// Average per-processor load.
	var sum, n int64
	_ = c.BulkWalk(ctx, mibs.HrProcessorLoad, func(p snmp.PDU) error {
		if v, ok := snmp.PDUInt64(p); ok {
			sum += v
			n++
		}
		return nil
	})
	if n > 0 {
		hr.CPULoadPct = int(sum / n)
	}

	// hrStorageTable: assemble per-index rows.
	type acc struct {
		descr             string
		typ               string
		units, size, used int64
	}
	rows := map[int32]*acc{}
	get := func(idx int32) *acc {
		a := rows[idx]
		if a == nil {
			a = &acc{typ: "other"}
			rows[idx] = a
		}
		return a
	}
	_ = c.BulkWalk(ctx, mibs.HrStorageEntry, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, mibs.HrStorageEntry)
		if !ok || len(idx) != 1 {
			return nil
		}
		a := get(int32(idx[0]))
		switch int(col) {
		case mibs.HrStorageColType:
			a.typ = storageType(snmp.PDUString(p))
		case mibs.HrStorageColDescr:
			a.descr = snmp.PDUString(p)
		case mibs.HrStorageColUnits:
			if v, ok := snmp.PDUInt64(p); ok {
				a.units = v
			}
		case mibs.HrStorageColSize:
			if v, ok := snmp.PDUInt64(p); ok {
				a.size = v
			}
		case mibs.HrStorageColUsed:
			if v, ok := snmp.PDUInt64(p); ok {
				a.used = v
			}
		}
		return nil
	})
	for idx, a := range rows {
		units := a.units
		if units <= 0 {
			units = 1
		}
		hr.Storage = append(hr.Storage, driver.StorageSnap{
			Index:      idx,
			Descr:      a.descr,
			Type:       a.typ,
			TotalBytes: a.size * units,
			UsedBytes:  a.used * units,
		})
	}
	return hr
}

// storageType maps an hrStorageType OID value to a normalized label.
func storageType(oid string) string {
	oid = strings.TrimPrefix(oid, ".")
	switch oid {
	case strings.TrimPrefix(mibs.HrStorageRAM, "."):
		return "ram"
	case strings.TrimPrefix(mibs.HrStorageFixedDisk, "."):
		return "disk"
	case strings.TrimPrefix(mibs.HrStorageVirtualMem, "."):
		return "virtual"
	}
	return "other"
}
