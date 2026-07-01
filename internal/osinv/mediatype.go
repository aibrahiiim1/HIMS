package osinv

import "strings"

// NormalizeMediaType canonicalizes a raw media hint (from a Windows MSFT_PhysicalDisk MediaType
// number / BusType, a Win32_DiskDrive model, or a Linux lsblk rotational/transport value) into
// one of "NVMe" | "SSD" | "HDD", or "" when it cannot be determined. It NEVER guesses: an
// unrecognized value returns "" so the UI honestly shows "unknown" rather than a fabricated type.
//
// hint is a collector-provided classification (e.g. "SSD", "4", "nvme", "rotational=0"); model is
// the disk model string, used as a secondary signal ("Samsung ... NVMe", "... SSD ...").
func NormalizeMediaType(hint, model string) string {
	h := strings.ToLower(strings.TrimSpace(hint))
	m := strings.ToLower(strings.TrimSpace(model))

	// NVMe is a bus/protocol and takes precedence — an NVMe device is always solid-state, and
	// operators care about the NVMe distinction.
	if strings.Contains(h, "nvme") || strings.Contains(m, "nvme") {
		return "NVMe"
	}
	// Explicit solid-state signals: the word, or MSFT_PhysicalDisk MediaType 4 (SSD),
	// or Linux non-rotational (rotational=0 / rota "0").
	if strings.Contains(h, "ssd") || strings.Contains(m, "ssd") ||
		h == "4" || strings.Contains(h, "rotational=0") || h == "0" || strings.Contains(h, "solid") {
		return "SSD"
	}
	// Spinning disk: the word, MediaType 3 (HDD), or Linux rotational=1.
	if strings.Contains(h, "hdd") || strings.Contains(h, "hard disk") || strings.Contains(m, "hard disk") ||
		h == "3" || strings.Contains(h, "rotational=1") || h == "1" || strings.Contains(h, "rotational") {
		return "HDD"
	}
	return ""
}
