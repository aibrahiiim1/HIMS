// Package mibpack implements the engine behind operator-managed MIB packs:
//   - Parse: lightweight extraction of module name / imports / tables / object
//     counts from raw MIB text (no external SMI compiler dependency).
//   - WalkTable: a generic SNMP table walk that returns rows grouped by row
//     index with their columns, plus an honest per-table status (supported /
//     empty / timeout / no_such_object / error) so partial coverage is reported,
//     never faked.
//
// The numeric mapping (which OID/column feeds which domain field) lives in the
// mib_pack_tables table — see internal/api. This package is pure logic + SNMP,
// unit-testable with an injected walker.
package mibpack

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/coralsearesorts/hims/internal/snmp"
)

// ParsedMIB is the result of a lightweight MIB-text parse.
type ParsedMIB struct {
	Module      string   `json:"module"`
	Imports     []string `json:"imports"`
	Tables      []string `json:"tables"`
	ObjectCount int      `json:"object_count"`
	TableCount  int      `json:"table_count"`
	Warnings    []string `json:"warnings"`
}

var (
	reModule  = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9-]+)\s+DEFINITIONS\b`)
	reTable   = regexp.MustCompile(`(?m)^\s*([a-zA-Z0-9]+Table)\s+OBJECT-TYPE`)
	reObject  = regexp.MustCompile(`(?m)^\s*[a-zA-Z0-9]+\s+OBJECT-TYPE`)
	reImports = regexp.MustCompile(`(?m)\bFROM\s+([A-Za-z0-9-]+)`)
)

// Parse extracts structure from raw MIB text. Best-effort + safe on any input.
func Parse(content string) ParsedMIB {
	p := ParsedMIB{}
	if m := reModule.FindStringSubmatch(content); m != nil {
		p.Module = m[1]
	} else {
		p.Warnings = append(p.Warnings, "no MODULE DEFINITIONS header found")
	}
	seenImp := map[string]bool{}
	for _, m := range reImports.FindAllStringSubmatch(content, -1) {
		if !seenImp[m[1]] {
			seenImp[m[1]] = true
			p.Imports = append(p.Imports, m[1])
		}
	}
	seenT := map[string]bool{}
	for _, m := range reTable.FindAllStringSubmatch(content, -1) {
		if !seenT[m[1]] {
			seenT[m[1]] = true
			p.Tables = append(p.Tables, m[1])
		}
	}
	sort.Strings(p.Tables)
	p.TableCount = len(p.Tables)
	p.ObjectCount = len(reObject.FindAllString(content, -1))
	return p
}

// TableStatus is the honest outcome of a table walk.
type TableStatus string

const (
	StatusSupported    TableStatus = "supported"     // ≥1 row returned
	StatusEmpty        TableStatus = "empty"          // walk ok, 0 rows
	StatusTimeout      TableStatus = "timeout"        // request timed out
	StatusNoSuchObject TableStatus = "no_such_object" // not implemented by agent
	StatusError        TableStatus = "error"
)

// Row is one tabular row: its index suffix + column(subID) → string value.
type Row struct {
	Index string            `json:"index"`
	Cols  map[uint32]string `json:"cols"`
	OIDs  map[uint32]string `json:"-"` // column → full OID (raw-row metadata)
}

// WalkResult is the outcome of walking one table root.
type WalkResult struct {
	RootOID string      `json:"root_oid"`
	Status  TableStatus `json:"status"`
	Rows    []Row       `json:"rows"`
	Count   int         `json:"count"`
	Detail  string      `json:"detail,omitempty"`
}

// WalkTable walks an SNMP table root (the table OID; the entry OID is rootOID.1)
// and groups the returned varbinds into rows by their index suffix. maxRows caps
// the result (0 = no cap). Pure: takes a connected snmp.Client.
func WalkTable(ctx context.Context, c snmp.Client, rootOID string, maxRows int) WalkResult {
	res := WalkResult{RootOID: rootOID}
	entryRoot := strings.TrimSuffix(rootOID, ".") + ".1" // tables have an Entry node at .1
	byIndex := map[string]*Row{}
	order := []string{}
	n := 0
	err := c.BulkWalk(ctx, rootOID, func(p snmp.PDU) error {
		col, idx, ok := snmp.ColumnAndIndex(p.OID, entryRoot)
		if !ok {
			return nil
		}
		idxStr := joinUint(idx)
		r := byIndex[idxStr]
		if r == nil {
			if maxRows > 0 && len(order) >= maxRows {
				return nil
			}
			r = &Row{Index: idxStr, Cols: map[uint32]string{}, OIDs: map[uint32]string{}}
			byIndex[idxStr] = r
			order = append(order, idxStr)
		}
		r.Cols[col] = snmp.PDUString(p)
		r.OIDs[col] = p.OID
		n++
		return nil
	})
	if err != nil {
		res.Detail = err.Error()
		switch {
		case strings.Contains(strings.ToLower(err.Error()), "timeout"):
			res.Status = StatusTimeout
		case strings.Contains(strings.ToLower(err.Error()), "no such") || strings.Contains(strings.ToLower(err.Error()), "nosuch"):
			res.Status = StatusNoSuchObject
		default:
			res.Status = StatusError
		}
		return res
	}
	for _, k := range order {
		res.Rows = append(res.Rows, *byIndex[k])
	}
	res.Count = len(res.Rows)
	if res.Count == 0 {
		res.Status = StatusEmpty
	} else {
		res.Status = StatusSupported
	}
	return res
}

func joinUint(a []uint32) string {
	parts := make([]string, len(a))
	for i, v := range a {
		parts[i] = utoa(v)
	}
	return strings.Join(parts, ".")
}

func utoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
