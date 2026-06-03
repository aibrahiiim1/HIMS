package mibparse

import "testing"

const sample = `
FORTINET-CORE-MIB DEFINITIONS ::= BEGIN
fortinet      OBJECT IDENTIFIER ::= { enterprises 12356 }
fnFortiGateMib OBJECT IDENTIFIER ::= { fortinet 101 }
fgSystem      OBJECT IDENTIFIER ::= { fnFortiGateMib 4 }

fgSysVersion OBJECT-TYPE
    SYNTAX      DisplayString
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "Firmware version."
    ::= { fgSystem 1 }

orphanNode OBJECT IDENTIFIER ::= { mysteryParent 9 }
END
`

func find(objs []Object, name string) (Object, bool) {
	for _, o := range objs {
		if o.Name == name {
			return o, true
		}
	}
	return Object{}, false
}

func TestParse_ResolvesEnterpriseChain(t *testing.T) {
	objs, err := Parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"fortinet":       "1.3.6.1.4.1.12356",
		"fnFortiGateMib": "1.3.6.1.4.1.12356.101",
		"fgSystem":       "1.3.6.1.4.1.12356.101.4",
		"fgSysVersion":   "1.3.6.1.4.1.12356.101.4.1",
	}
	for name, want := range cases {
		o, ok := find(objs, name)
		if !ok {
			t.Fatalf("%s not parsed", name)
		}
		if o.OID != want {
			t.Errorf("%s OID = %q; want %q", name, o.OID, want)
		}
		if o.Unresolved {
			t.Errorf("%s flagged unresolved", name)
		}
	}
}

func TestParse_ObjectTypeKindAndSyntax(t *testing.T) {
	objs, _ := Parse(sample)
	o, _ := find(objs, "fgSysVersion")
	if o.Kind != "object" {
		t.Errorf("kind = %q; want object", o.Kind)
	}
	if o.Syntax != "DisplayString" {
		t.Errorf("syntax = %q; want DisplayString", o.Syntax)
	}
}

func TestParse_UnresolvedParentKept(t *testing.T) {
	objs, _ := Parse(sample)
	o, ok := find(objs, "orphanNode")
	if !ok {
		t.Fatal("orphanNode dropped; should be kept as unresolved")
	}
	if !o.Unresolved || o.Parent != "mysteryParent" {
		t.Errorf("orphan = %+v; want unresolved with parent mysteryParent", o)
	}
}

func TestParse_Empty(t *testing.T) {
	if _, err := Parse("   "); err == nil {
		t.Fatal("empty input should error")
	}
}
