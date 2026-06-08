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
	StatusSupported    TableStatus = "supported"      // ≥1 row returned
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
// the number of rows (0 = no cap). Pure: takes a connected snmp.Client. It is a
// thin wrapper over RawWalk + GroupRows so raw capture and table interpretation
// share one walk + one (boundary-correct) parser.
func WalkTable(ctx context.Context, c snmp.Client, rootOID string, maxRows int) WalkResult {
	res := WalkResult{RootOID: rootOID}
	vars, status, detail := RawWalk(ctx, c, rootOID, 0)
	if status == StatusTimeout || status == StatusNoSuchObject || status == StatusError {
		res.Status, res.Detail = status, detail
		return res
	}
	res.Rows = GroupRows(vars, rootOID, maxRows)
	res.Count = len(res.Rows)
	if res.Count == 0 {
		res.Status = StatusEmpty
	} else {
		res.Status = StatusSupported
	}
	return res
}

// GroupRows interprets raw varbinds as an SNMP table: it splits each OID into
// (column, index) at the entry node (rootOID + ".1") and groups columns by
// index into rows. Varbinds that don't sit under the entry (sibling subtrees)
// are ignored — correct now that ColumnAndIndex matches on sub-id boundaries.
// maxRows caps distinct rows (0 = no cap). Cell values are the rendered display
// strings from RawWalk (MAC / IP / hex / int / string).
func GroupRows(vars []RawVar, rootOID string, maxRows int) []Row {
	entryRoot := strings.TrimSuffix(rootOID, ".") + ".1"
	byIndex := map[string]*Row{}
	var order []string
	for _, v := range vars {
		col, idx, ok := snmp.ColumnAndIndex(v.OID, entryRoot)
		if !ok {
			continue
		}
		idxStr := joinUint(idx)
		r := byIndex[idxStr]
		if r == nil {
			if maxRows > 0 && len(order) >= maxRows {
				continue
			}
			r = &Row{Index: idxStr, Cols: map[uint32]string{}, OIDs: map[uint32]string{}}
			byIndex[idxStr] = r
			order = append(order, idxStr)
		}
		r.Cols[col] = v.Value
		r.OIDs[col] = v.OID
	}
	out := make([]Row, 0, len(order))
	for _, k := range order {
		out = append(out, *byIndex[k])
	}
	return out
}

// OIDSuffix returns the sub-identifiers of oid below root as a dotted string
// ("" if oid is not under root). Used to label raw rows with their index.
func OIDSuffix(oid, root string) string {
	suffix, ok := snmp.TrimOIDPrefix(oid, root)
	if !ok {
		return ""
	}
	return joinUint(suffix)
}

// RawVar is one varbind captured by a raw subtree walk (no table assumption).
type RawVar struct {
	OID   string `json:"oid"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// RawWalk walks the ENTIRE subtree under root and returns every varbind with
// its SNMP type and a human-rendered value (MAC / IP / hex / int / string).
// Unlike WalkTable it makes no table/entry/column/index assumption, so it
// faithfully captures whatever a given firmware exposes — the basis of the MIB
// Explorer and of honest raw capture. maxRows caps the result (0 = no cap).
func RawWalk(ctx context.Context, c snmp.Client, root string, maxRows int) (vars []RawVar, status TableStatus, detail string) {
	n := 0
	err := c.BulkWalk(ctx, root, func(p snmp.PDU) error {
		if maxRows > 0 && n >= maxRows {
			return nil
		}
		vars = append(vars, RawVar{OID: p.OID, Type: snmp.PDUTypeName(p.Type), Value: snmp.PDUDisplay(p)})
		n++
		return nil
	})
	if err != nil {
		return vars, classifyWalkErr(err), err.Error()
	}
	if len(vars) == 0 {
		return vars, StatusEmpty, ""
	}
	return vars, StatusSupported, ""
}

// classifyWalkErr maps a walk error to an honest table status.
func classifyWalkErr(err error) TableStatus {
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "timeout"):
		return StatusTimeout
	case strings.Contains(low, "no such") || strings.Contains(low, "nosuch"):
		return StatusNoSuchObject
	default:
		return StatusError
	}
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
