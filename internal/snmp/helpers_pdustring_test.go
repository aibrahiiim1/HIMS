package snmp

import "testing"

// A noSuchObject / noSuchInstance / endOfMibView PDU, and a nil value, are the
// ABSENCE of a value — PDUString must return "" for them, never the Go literal
// "<nil>". Regression guard: treating those as data let HP CPQ OIDs that a Dell
// iDRAC answers with noSuchObject be read as a real "HPE" identity (a clobber).
func TestPDUString_ErrorsAndNilAreEmpty(t *testing.T) {
	cases := []struct {
		name string
		pdu  PDU
	}{
		{"noSuchObject", PDU{Type: TypeNoSuchObject, Value: nil}},
		{"noSuchInstance", PDU{Type: TypeNoSuchInstance, Value: nil}},
		{"endOfMibView", PDU{Type: TypeEndOfMIBView, Value: nil}},
		{"nil value octet", PDU{Type: TypeOctetString, Value: nil}},
	}
	for _, c := range cases {
		if got := PDUString(c.pdu); got != "" {
			t.Errorf("%s: PDUString = %q, want \"\"", c.name, got)
		}
	}
}

func TestPDUString_RealValues(t *testing.T) {
	if got := PDUString(PDU{Type: TypeOctetString, Value: []byte("ProLiant DL380 Gen10")}); got != "ProLiant DL380 Gen10" {
		t.Errorf("octet string = %q", got)
	}
	if got := PDUString(PDU{Type: TypeOctetString, Value: "iLO 5"}); got != "iLO 5" {
		t.Errorf("string = %q", got)
	}
}
