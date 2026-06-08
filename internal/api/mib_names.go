package api

import "strings"

// Documented HiPath/Extreme HWC-MIB object names, keyed by numeric OID. Used by
// the MIB Explorer to label captured subtrees with their real symbolic names
// where the MIB documents them. Firmware that exposes OIDs the bundled MIB does
// NOT define (e.g. mobileUnits.10..13 on fw 10.05) is left unlabelled — honest,
// "(undocumented in pack)", never guessed.
//
// Names derived from HIPATH-WIRELESS-HWC-MIB (hiPathWirelessMgmt = 1.3.6.1.4.1.5624.1.2).
var hiPathOIDNames = map[string]string{
	"1.3.6.1.4.1.5624.1.2":       "hiPathWirelessMgmt",
	"1.3.6.1.4.1.5624.1.2.2":     "hiPathWirelessController",
	"1.3.6.1.4.1.5624.1.2.2.7":   "controllerStats",
	"1.3.6.1.4.1.5624.1.2.2.10":  "dashboard",
	"1.3.6.1.4.1.5624.1.2.3":     "virtualNetworks",
	"1.3.6.1.4.1.5624.1.2.3.4":   "wlan",
	"1.3.6.1.4.1.5624.1.2.3.4.4": "wlanTable",
	"1.3.6.1.4.1.5624.1.2.4":     "topology",
	"1.3.6.1.4.1.5624.1.2.4.1":   "topologyConfig",
	"1.3.6.1.4.1.5624.1.2.4.2":   "topologyStat",
	"1.3.6.1.4.1.5624.1.2.5":     "accessPoints",
	"1.3.6.1.4.1.5624.1.2.5.1":   "apConfigObjects",
	"1.3.6.1.4.1.5624.1.2.5.2":   "apStatsObjects",
	"1.3.6.1.4.1.5624.1.2.6":     "mobileUnits",
	"1.3.6.1.4.1.5624.1.2.6.1":   "mobileUnitCount",
	"1.3.6.1.4.1.5624.1.2.6.2":   "muTable",
	"1.3.6.1.4.1.5624.1.2.6.2.1": "muEntry",
	// muEntry columns (muTable .6.2.1.<col>) — the documented client table.
	"1.3.6.1.4.1.5624.1.2.6.2.1.1":  "muMACAddress",
	"1.3.6.1.4.1.5624.1.2.6.2.1.2":  "muIPAddress",
	"1.3.6.1.4.1.5624.1.2.6.2.1.3":  "muUser",
	"1.3.6.1.4.1.5624.1.2.6.2.1.4":  "muState",
	"1.3.6.1.4.1.5624.1.2.6.2.1.5":  "muAPSerialNo",
	"1.3.6.1.4.1.5624.1.2.6.2.1.6":  "muVnsSSID",
	"1.3.6.1.4.1.5624.1.2.6.2.1.7":  "muTxPackets",
	"1.3.6.1.4.1.5624.1.2.6.2.1.8":  "muRxPackets",
	"1.3.6.1.4.1.5624.1.2.6.2.1.9":  "muTxOctets",
	"1.3.6.1.4.1.5624.1.2.6.2.1.10": "muRxOctets",
	"1.3.6.1.4.1.5624.1.2.6.2.1.11": "muDuration",
	"1.3.6.1.4.1.5624.1.2.6.2.1.12": "muAPName",
	"1.3.6.1.4.1.5624.1.2.6.2.1.13": "muTopologyName",
	"1.3.6.1.4.1.5624.1.2.6.2.1.14": "muPolicyName",
	"1.3.6.1.4.1.5624.1.2.6.2.1.15": "muDefaultCoS",
	"1.3.6.1.4.1.5624.1.2.6.2.1.16": "muConnectionProtocol",
	"1.3.6.1.4.1.5624.1.2.6.2.1.17": "muConnectionCapability",
	"1.3.6.1.4.1.5624.1.2.6.2.1.18": "muWLANID",
	"1.3.6.1.4.1.5624.1.2.6.2.1.19": "muBSSIDMac",
	"1.3.6.1.4.1.5624.1.2.6.2.1.20": "muDot11ConnectionCapability",
	"1.3.6.1.4.1.5624.1.2.7":        "associations",
	"1.3.6.1.4.1.5624.1.2.8":        "protocols",
	"1.3.6.1.4.1.5624.1.2.9":        "logNotifications",
	"1.3.6.1.4.1.5624.1.2.10":       "sites",
	"1.3.6.1.4.1.5624.1.2.11":       "widsWips",
	"1.3.6.1.4.1.5624.1.2.19":       "apNotifications",
}

// mibOIDName returns the best documented name for an OID: exact match if known,
// else "<nearestParentName>.<remaining>" so a column under a known table reads
// like "muEntry.+3" rather than a bare number. Returns "" when nothing matches.
func mibOIDName(oid string) string {
	oid = strings.TrimPrefix(oid, ".")
	if n, ok := hiPathOIDNames[oid]; ok {
		return n
	}
	// Longest documented prefix wins.
	best, bestName := "", ""
	for k, v := range hiPathOIDNames {
		if (oid == k || strings.HasPrefix(oid, k+".")) && len(k) > len(best) {
			best, bestName = k, v
		}
	}
	if best == "" {
		return ""
	}
	suffix := strings.TrimPrefix(oid[len(best):], ".")
	if suffix == "" {
		return bestName
	}
	return bestName + "." + suffix
}
