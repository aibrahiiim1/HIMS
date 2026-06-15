package classify

import (
	"fmt"

	"github.com/coralsearesorts/hims/internal/domain"
)

// SwitchSignals are the inputs for switch-subtype classification, gathered from
// a switch's collected inventory. Pure — no network/DB here.
type SwitchSignals struct {
	Vendor     string
	Model      string
	Hostname   string
	PortsTotal int
	PortsUp    int
	VLANs      int
	// Uplinks is the number of this switch's ports that face other infrastructure
	// (i.e. have an LLDP/CDP neighbor). It's the access↔distribution↔core
	// discriminator: a leaf access switch has 1–2; an aggregation switch has many.
	Uplinks int
}

// SwitchSubtype scores an access / distribution / core (or unknown) switch
// subtype from collected inventory. It is honest and transparent: it returns
// unknown_switch at low confidence when the evidence is too weak to tell, and an
// evidence trail citing the exact counts that drove the decision. Subtype values:
//
//	access_switch | distribution_switch | core_switch | unknown_switch
func SwitchSubtype(sig SwitchSignals) (subtype string, confidence int, evidence []domain.ClassificationEvidence) {
	add := func(signal, sub string, conf int) {
		evidence = append(evidence, domain.ClassificationEvidence{
			Source: "switch_topology", Signal: signal, Category: string(domain.CatSwitch),
			Subtype: sub, Confidence: conf,
		})
	}

	// Always record the counts as a transparent trail (confidence 0 = informational).
	if sig.Model != "" {
		add("model "+sig.Model, "", 0)
	}
	if sig.PortsTotal > 0 {
		add(fmt.Sprintf("%d ports (%d up)", sig.PortsTotal, sig.PortsUp), "", 0)
	}
	if sig.VLANs > 0 {
		add(fmt.Sprintf("%d VLANs configured", sig.VLANs), "", 0)
	}
	add(fmt.Sprintf("%d infrastructure uplinks (LLDP/CDP neighbours)", sig.Uplinks), "", 0)

	// No interface inventory at all → cannot tell; weak unknown (never fabricate).
	if sig.PortsTotal == 0 {
		add("no interface inventory collected — cannot determine role", "unknown_switch", 30)
		return "unknown_switch", 30, evidence
	}

	switch {
	case sig.Uplinks >= 7:
		add(fmt.Sprintf("aggregates %d infrastructure links — backbone role", sig.Uplinks), "core_switch", 62)
		return "core_switch", 62, evidence
	case sig.Uplinks >= 3:
		add(fmt.Sprintf("aggregates %d infrastructure links — distribution role", sig.Uplinks), "distribution_switch", 66)
		return "distribution_switch", 66, evidence
	case sig.Uplinks >= 1:
		add(fmt.Sprintf("%d uplink(s), edge-port dominant — access role", sig.Uplinks), "access_switch", 74)
		return "access_switch", 74, evidence
	default:
		add("edge ports, no infrastructure uplinks detected — standalone access role", "access_switch", 68)
		return "access_switch", 68, evidence
	}
}
