// Package classify is HIMS's evidence-based device classifier. It is pure (no
// network, no DB) so the whole policy is unit-testable: discovery probes gather
// raw observations, the rule functions in rules.go turn each observation into
// one or more domain.ClassificationEvidence, and FromEvidence merges the
// evidence into a single (category, os_family, subtype, confidence) decision
// plus the supporting trail. The trail is what an operator sees and what makes
// a low-confidence guess auditable instead of opaque.
package classify

import (
	"sort"

	"github.com/coralsearesorts/hims/internal/domain"
)

// Result is the merged classification decision for a device.
type Result struct {
	Category   string                          // domain.Cat* (a DeviceCategory value)
	OSFamily   string                          // domain.OSFamily* ("" = unknown)
	Subtype    string                          // device_class subtype ("" = none)
	Confidence int                             // 0..100, the winning category's merged score
	Evidence   []domain.ClassificationEvidence // contributing signals, strongest first
}

// corroborationBonus is added per *additional independent source* that agrees on
// a category, capped so a pile of weak signals can't beat one definitive signal
// outright but still rewards agreement.
const corroborationBonus = 5

// FromEvidence merges raw evidence into one decision. The winning category is
// the one whose strongest single signal — plus a small bonus for each extra
// independent corroborating source — scores highest. OS family and subtype are
// taken from the highest-confidence evidence that names each (a Linux SSH banner
// pins os_family regardless of which category wins). Empty input → unknown.
func FromEvidence(ev []domain.ClassificationEvidence) Result {
	out := Result{Category: string(domain.CatUnknown)}
	if len(ev) == 0 {
		return out
	}

	// Sort a copy by confidence desc (then source) for deterministic picks +
	// display order; never mutate the caller's slice.
	sorted := make([]domain.ClassificationEvidence, len(ev))
	copy(sorted, ev)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Confidence != sorted[j].Confidence {
			return sorted[i].Confidence > sorted[j].Confidence
		}
		return sorted[i].Source < sorted[j].Source
	})
	out.Evidence = sorted

	// Score each category: strongest signal + corroboration across distinct sources.
	type agg struct {
		best    int
		sources map[string]bool
	}
	cats := map[string]*agg{}
	for _, e := range sorted {
		if e.Category == "" {
			continue
		}
		a := cats[e.Category]
		if a == nil {
			a = &agg{sources: map[string]bool{}}
			cats[e.Category] = a
		}
		if e.Confidence > a.best {
			a.best = e.Confidence
		}
		a.sources[e.Source] = true
	}
	bestScore := 0
	for c, a := range cats {
		score := a.best + (len(a.sources)-1)*corroborationBonus
		if score > 100 {
			score = 100
		}
		if score > bestScore || (score == bestScore && c < out.Category) {
			out.Category, bestScore = c, score
		}
	}
	out.Confidence = bestScore

	// OS family + subtype: highest-confidence evidence that names one (sorted desc).
	for _, e := range sorted {
		if out.OSFamily == "" && e.OSFamily != "" {
			out.OSFamily = e.OSFamily
		}
		if out.Subtype == "" && e.Subtype != "" {
			out.Subtype = e.Subtype
		}
	}

	// Derive a sensible subtype when no signal supplied one but the category +
	// OS family imply it (server + windows/linux → *_server).
	if out.Subtype == "" {
		out.Subtype = deriveSubtype(out.Category, out.OSFamily)
	}
	return out
}

// deriveSubtype fills the device_class subtype from category+os_family when no
// evidence named one explicitly. Returns "" when nothing sensible applies.
func deriveSubtype(category, osFamily string) string {
	if category == string(domain.CatServer) {
		switch osFamily {
		case domain.OSFamilyWindows:
			return "windows_server"
		case domain.OSFamilyLinux:
			return "linux_server"
		}
	}
	if category == string(domain.CatEndpoint) {
		switch osFamily {
		case domain.OSFamilyWindows:
			return "windows_workstation"
		case domain.OSFamilyLinux:
			return "linux_workstation"
		case domain.OSFamilyMacOS:
			return "mac_workstation"
		}
	}
	return ""
}
