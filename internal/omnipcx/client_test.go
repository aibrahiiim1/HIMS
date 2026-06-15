package omnipcx

import "testing"

// gridFixture mirrors a real OXE mgr "All instances: Users" grid page (R7.1):
// ANSI/VT100 framing, a header line carrying the total count, then directory
// numbers laid out in a column grid.
const gridFixture = "\x1b[?1h\x1b=\x1b[H\x1b[J   \x1b[0mlq\x1b[0m[ 786 ] Instances: Users\x1b[0mqqqqqqk\n" +
	" \x1b[0mx ->\x1b[0;1;7m 1111  \x1b[m    210101    210119    211108    21204   \x1b[0mx\n" +
	" \x1b[0mx    1989      210102    210210    211109    212101  \x1b[0mx\n" +
	" \x1b[0mx    210103    210211    210301    212102            \x1b[0mx\n" +
	" \x1b[0mmqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqj\x1b[H\n"

func TestParseGridNumbers(t *testing.T) {
	got := parseGridNumbers(gridFixture)
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, want := range []string{"1111", "1989", "210101", "210102", "210103", "210119", "211108", "21204", "212101", "210301"} {
		if !set[want] {
			t.Errorf("expected directory number %q in parsed grid; got %v", want, got)
		}
	}
	// The header total must NOT be mistaken for an extension.
	if set["786"] {
		t.Errorf("header count 786 must not be parsed as a directory number")
	}
}

func TestInstancesHeaderParse(t *testing.T) {
	if m := instancesRe.FindStringSubmatch("...[ 786 ] Instances: Users..."); m == nil || m[1] != "786" {
		t.Fatalf("instancesRe did not extract the total count: %v", m)
	}
}

func TestStripIAC(t *testing.T) {
	// IAC DO ECHO (255 253 1) should be consumed and answered WONT (255 252 1).
	in := []byte{'h', 'i', 255, 253, 1, '!'}
	data, reply := stripIAC(in)
	if string(data) != "hi!" {
		t.Fatalf("data = %q; want %q", data, "hi!")
	}
	if len(reply) != 3 || reply[0] != 255 || reply[1] != 252 || reply[2] != 1 {
		t.Fatalf("reply = %v; want IAC WONT ECHO (255 252 1)", reply)
	}
}
