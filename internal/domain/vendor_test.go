package domain

import "testing"

func TestCanonicalVendor(t *testing.T) {
	cases := map[string]string{
		"HIKVISION": "Hikvision",
		"hikvision": "Hikvision",
		"Hikvision": "Hikvision",
		"Hangzhou Hikvision Digital Technology Co., Ltd": "Hikvision",
		"  hikvision  ":    "Hikvision", // trimmed
		"CISCO":            "Cisco",
		"Extreme":          "Extreme Networks",
		"":                 "",                 // never invents a value
		"Some Other Brand": "Some Other Brand", // unknown passes through unchanged
	}
	for in, want := range cases {
		if got := CanonicalVendor(in); got != want {
			t.Errorf("CanonicalVendor(%q) = %q, want %q", in, got, want)
		}
	}
}
