package snmp

import "testing"

func TestOIDPrefixMatchBoundary(t *testing.T) {
	const root = "1.3.6.1.4.1.5624.1.2.6.1"
	cases := []struct {
		oid  string
		want bool
	}{
		{"1.3.6.1.4.1.5624.1.2.6.1", true},        // exact
		{"1.3.6.1.4.1.5624.1.2.6.1.5.1.10.1", true}, // genuine child
		{"1.3.6.1.4.1.5624.1.2.6.12.1.0", false},  // sibling — must NOT match (was the bug)
		{"1.3.6.1.4.1.5624.1.2.6.11", false},      // sibling
		{"1.3.6.1.4.1.5624.1.2.6.13.2.0", false},  // sibling
		{".1.3.6.1.4.1.5624.1.2.6.1.2.0", true},   // leading dot tolerated
	}
	for _, c := range cases {
		if got := HasOIDPrefix(c.oid, root); got != c.want {
			t.Errorf("HasOIDPrefix(%q, %q) = %v, want %v", c.oid, root, got, c.want)
		}
	}
}

func TestColumnAndIndexRejectsSibling(t *testing.T) {
	entryRoot := "1.3.6.1.4.1.5624.1.2.6.1"
	// A sibling subtree must NOT be parsed as a column of this entry.
	if _, _, ok := ColumnAndIndex("1.3.6.1.4.1.5624.1.2.6.12.1.0", entryRoot); ok {
		t.Fatal("ColumnAndIndex matched a sibling subtree (.6.12) against entry root .6.1")
	}
	// A genuine columnar OID under the entry parses into (column, index).
	col, idx, ok := ColumnAndIndex("1.3.6.1.4.1.5624.1.2.6.1.5.1.10.7", entryRoot)
	if !ok || col != 5 {
		t.Fatalf("ColumnAndIndex genuine child: ok=%v col=%d (want ok, col=5)", ok, col)
	}
	if len(idx) == 0 {
		t.Fatal("expected a non-empty index for a genuine columnar OID")
	}
}
