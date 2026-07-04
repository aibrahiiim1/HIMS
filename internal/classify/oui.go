package classify

import (
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
)

// OUIClassify maps a device's MAC OUI (the vendor half of its physical address) to a
// vendor and, for UNAMBIGUOUS single-category vendors, a device category. This exists
// to correct weak port-only guesses: in a POS/hotel environment a Posiflex terminal or
// an Epson receipt printer may expose only SIP/5060 during a scan (no SNMP/HTTP), which
// the port classifier reads as an IP phone. The MAC OUI — which HIMS already learns from
// switch ARP tables — is strong vendor evidence that a bare port is not.
//
// ONLY vendors that build essentially one network-device class are mapped to a category:
//   - Seiko Epson  -> printer  (receipt/label/office printers)
//   - Posiflex     -> pos      (point-of-sale terminals)
//
// Multi-category vendors (Cisco, HPE, Alcatel-Lucent — switches AND phones AND servers)
// are deliberately NOT category-mapped: their OUI identifies the vendor, never the type.
// Confidence is moderate (70/72): it beats the weak bare-port guesses (SIP 5060 -> 55,
// RDP -> 45) but never overrides an authenticated SNMP/driver classification (>= 78).
func OUIClassify(mac string) (vendor string, cat domain.DeviceCategory, confidence int) {
	oui := normalizeOUI(mac)
	if oui == "" {
		return "", "", 0
	}
	if v, ok := ouiVendorCategory[oui]; ok {
		return v.vendor, v.cat, v.conf
	}
	return "", "", 0
}

type ouiEntry struct {
	vendor string
	cat    domain.DeviceCategory
	conf   int
}

// normalizeOUI returns the first three MAC octets as uppercase "AA:BB:CC", or "".
func normalizeOUI(mac string) string {
	mac = strings.TrimSpace(mac)
	if mac == "" {
		return ""
	}
	sep := ":"
	if strings.Contains(mac, "-") {
		sep = "-"
	}
	parts := strings.Split(mac, sep)
	if len(parts) < 3 {
		// bare hex like "005057" — split into octet pairs
		clean := strings.ToUpper(strings.ReplaceAll(mac, ".", ""))
		if len(clean) >= 6 {
			return clean[0:2] + ":" + clean[2:4] + ":" + clean[4:6]
		}
		return ""
	}
	return strings.ToUpper(parts[0] + ":" + parts[1] + ":" + parts[2])
}

func epson(oui string) (string, ouiEntry)    { return oui, ouiEntry{"Epson", domain.CatPrinter, 70} }
func posiflex(oui string) (string, ouiEntry) { return oui, ouiEntry{"Posiflex", domain.CatPOS, 72} }

// ouiVendorCategory is the curated OUI -> vendor/category table (extend as needed; keep
// to single-category vendors only). Sources: IEEE MA-L registrations.
var ouiVendorCategory = func() map[string]ouiEntry {
	m := map[string]ouiEntry{}
	add := func(oui string, e ouiEntry) { m[oui] = e }
	// Seiko Epson Corporation — all 22 MA-L prefixes (2000..2025).
	for _, o := range []string{
		"00:00:48", "00:26:AB", "38:1A:52", "38:9D:92", "44:D2:44", "50:57:9C",
		"58:05:D9", "64:C6:D2", "64:EB:8C", "68:55:D4", "9C:AE:D3", "A4:D7:3C",
		"A4:EE:57", "AC:18:26", "B0:E8:92", "BC:C8:CC", "D4:80:8B", "DC:83:BF",
		"DC:CD:2F", "E0:BB:9E", "F8:25:51", "F8:D0:27",
	} {
		add(epson(o))
	}
	// Posiflex Inc. — POS terminals.
	add(posiflex("00:19:17"))
	return m
}()
