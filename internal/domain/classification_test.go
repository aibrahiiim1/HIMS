package domain

import "testing"

func TestMarshalEvidence_EmptyIsArrayNotNull(t *testing.T) {
	// The classification_evidence column has a CHECK (jsonb_typeof = 'array');
	// a nil/empty slice MUST serialise to "[]", never "null".
	for _, in := range [][]ClassificationEvidence{nil, {}} {
		b, err := MarshalEvidence(in)
		if err != nil {
			t.Fatalf("MarshalEvidence(%v) error: %v", in, err)
		}
		if string(b) != "[]" {
			t.Errorf("MarshalEvidence(%v) = %q, want %q", in, b, "[]")
		}
	}
}

func TestMarshalUnmarshalEvidence_RoundTrip(t *testing.T) {
	in := []ClassificationEvidence{
		{Source: EvidenceSourceISAPI, Signal: "deviceType=DVR", Category: "nvr", OSFamily: OSFamilyEmbedded, Confidence: 85},
		{Source: EvidenceSourceSSHBanner, Signal: "SSH-2.0-OpenSSH_8.0", OSFamily: OSFamilyLinux, Subtype: "linux_server", Confidence: 60},
	}
	b, err := MarshalEvidence(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalEvidence(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round-trip len = %d, want %d", len(out), len(in))
	}
	if out[0].Source != EvidenceSourceISAPI || out[0].Category != "nvr" || out[0].Confidence != 85 {
		t.Errorf("evidence[0] mangled: %+v", out[0])
	}
	if out[1].OSFamily != OSFamilyLinux || out[1].Subtype != "linux_server" {
		t.Errorf("evidence[1] mangled: %+v", out[1])
	}
}

func TestUnmarshalEvidence_NullAndEmpty(t *testing.T) {
	for _, in := range []string{"", "null", "[]"} {
		out, err := UnmarshalEvidence([]byte(in))
		if err != nil {
			t.Errorf("UnmarshalEvidence(%q) error: %v", in, err)
		}
		if len(out) != 0 {
			t.Errorf("UnmarshalEvidence(%q) = %v, want empty", in, out)
		}
	}
}
