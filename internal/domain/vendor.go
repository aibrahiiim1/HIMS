package domain

import "strings"

// canonicalVendors maps a lowercased vendor string to its preferred display
// casing. Discovery sources report the same manufacturer with different casing
// or legal-name forms (ISAPI "Hikvision" vs SNMP "HIKVISION" vs ONVIF "Hangzhou
// Hikvision Digital Technology Co., Ltd"), which otherwise splits one vendor into
// several entries in inventory/reporting. Only known vendors are rewritten;
// anything else is returned trimmed-but-unchanged so we never mangle a value.
var canonicalVendors = map[string]string{
	"hikvision": "Hikvision",
	"hangzhou hikvision digital technology co., ltd": "Hikvision",
	"hangzhou hikvision digital technology":          "Hikvision",
	"dahua":            "Dahua",
	"uniview":          "Uniview",
	"axis":             "Axis",
	"cisco":            "Cisco",
	"cisco systems":    "Cisco",
	"huawei":           "Huawei",
	"aruba":            "Aruba",
	"aruba networks":   "Aruba",
	"hp":               "HP",
	"hpe":              "HPE",
	"fortinet":         "Fortinet",
	"mikrotik":         "MikroTik",
	"ubiquiti":         "Ubiquiti",
	"ruckus":           "Ruckus",
	"extreme":          "Extreme Networks",
	"extreme networks": "Extreme Networks",
	"juniper":          "Juniper",
	"juniper networks": "Juniper",
}

// CanonicalVendor returns the preferred casing for a known vendor, or the input
// trimmed unchanged for unknown vendors. It never blanks a non-empty value, so
// it's safe to apply at every vendor-write chokepoint.
func CanonicalVendor(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return t
	}
	if c, ok := canonicalVendors[strings.ToLower(t)]; ok {
		return c
	}
	return t
}
