// Package mibparse is a pragmatic SMIv2 MIB reader. It does NOT implement the
// full ASN.1 grammar; it extracts the two assignments that matter for an OID
// library — OBJECT IDENTIFIER nodes and OBJECT-TYPE leaves — and resolves each
// symbolic `{ parent N }` to a dotted numeric OID using a seeded base table
// plus the names defined within the file.
//
// The value is the mapping (symbol → OID → metric), so a partial-but-correct
// resolve is useful: names that can't be reduced to a numeric root are
// returned Unresolved=true rather than dropped, so the operator sees them.
package mibparse

import (
	"fmt"
	"regexp"
	"strings"
)

// Object is one parsed MIB node.
type Object struct {
	Name       string
	OID        string // dotted numeric, e.g. "1.3.6.1.4.1.12356"
	Syntax     string // OBJECT-TYPE SYNTAX, empty for pure nodes
	Kind       string // "node" (OBJECT IDENTIFIER) | "object" (OBJECT-TYPE)
	Parent     string // symbolic parent (for diagnostics)
	Unresolved bool   // true if the OID could not be reduced to a numeric root
}

// baseRoots seeds the well-known SMI tree so enterprise MIBs resolve against
// standard anchors without needing SNMPv2-SMI in the upload.
var baseRoots = map[string]string{
	"iso": "1", "org": "1.3", "dod": "1.3.6", "internet": "1.3.6.1",
	"directory": "1.3.6.1.1", "mgmt": "1.3.6.1.2", "mib-2": "1.3.6.1.2.1",
	"transmission": "1.3.6.1.2.1.10", "experimental": "1.3.6.1.3",
	"private": "1.3.6.1.4", "enterprises": "1.3.6.1.4.1",
	"system": "1.3.6.1.2.1.1", "interfaces": "1.3.6.1.2.1.2", "snmpV2": "1.3.6.1.6",
}

var (
	// NAME OBJECT IDENTIFIER ::= { parent N }   (or pure-numeric { 1 3 6 ... })
	reNode = regexp.MustCompile(`(?m)^\s*([A-Za-z][\w-]*)\s+OBJECT\s+IDENTIFIER\s*::=\s*\{\s*([^}]+?)\s*\}`)
	// NAME OBJECT-TYPE ... ::= { parent N }     (body may span lines)
	reObject = regexp.MustCompile(`([A-Za-z][\w-]*)\s+OBJECT-TYPE\b([\s\S]*?)::=\s*\{\s*([^}]+?)\s*\}`)
	reSyntax = regexp.MustCompile(`SYNTAX\s+([^\n]+)`)
	reToken  = regexp.MustCompile(`([A-Za-z][\w-]*)\s*\(\s*(\d+)\s*\)|([A-Za-z][\w-]*)|(\d+)`)
)

type rawDef struct {
	name   string
	tokens string // the inside of { ... }
	syntax string
	kind   string
}

// Parse reads MIB text and returns the resolved objects.
func Parse(text string) ([]Object, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("mibparse: empty input")
	}
	defs := map[string]*rawDef{}
	var order []string

	for _, m := range reNode.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if _, dup := defs[name]; !dup {
			order = append(order, name)
		}
		defs[name] = &rawDef{name: name, tokens: m[2], kind: "node"}
	}
	for _, m := range reObject.FindAllStringSubmatch(text, -1) {
		name := m[1]
		syntax := ""
		if s := reSyntax.FindStringSubmatch(m[2]); s != nil {
			syntax = strings.TrimSpace(s[1])
		}
		if _, dup := defs[name]; !dup {
			order = append(order, name)
		}
		defs[name] = &rawDef{name: name, tokens: m[3], syntax: syntax, kind: "object"}
	}

	resolved := map[string]string{}
	var out []Object
	for _, name := range order {
		d := defs[name]
		oid, parent, ok := resolve(d, defs, resolved, map[string]bool{})
		out = append(out, Object{
			Name: name, OID: oid, Syntax: d.syntax, Kind: d.kind,
			Parent: parent, Unresolved: !ok,
		})
	}
	return out, nil
}

// resolve reduces a def's `{ tokens }` to a dotted numeric OID. tokens are a
// mix of `parent`, `parent(N)`, bare `N`, and a leading symbolic parent.
func resolve(d *rawDef, defs map[string]*rawDef, memo map[string]string, seen map[string]bool) (oid, parent string, ok bool) {
	if v, done := memo[d.name]; done {
		return v, "", v != ""
	}
	if seen[d.name] {
		return "", "", false // cycle guard
	}
	seen[d.name] = true

	var parts []string
	first := true
	for _, t := range reToken.FindAllStringSubmatch(d.tokens, -1) {
		switch {
		case t[1] != "": // name(N) — take the number
			parts = append(parts, t[2])
			first = false
		case t[3] != "": // bare symbol — must be the (leading) parent
			sym := t[3]
			if first {
				parent = sym
				base := lookupBase(sym, defs, memo, seen)
				if base == "" {
					memo[d.name] = ""
					return "", parent, false
				}
				parts = append(parts, base)
			}
			first = false
		case t[4] != "": // bare number
			parts = append(parts, t[4])
			first = false
		}
	}
	res := strings.Join(parts, ".")
	memo[d.name] = res
	return res, parent, res != ""
}

// lookupBase resolves a symbolic parent to its numeric prefix, via the base
// table or another in-file definition.
func lookupBase(sym string, defs map[string]*rawDef, memo map[string]string, seen map[string]bool) string {
	if b, ok := baseRoots[sym]; ok {
		return b
	}
	if d, ok := defs[sym]; ok {
		b, _, ok := resolve(d, defs, memo, seen)
		if ok {
			return b
		}
	}
	return ""
}
