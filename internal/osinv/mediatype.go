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

	// A VM guest disk has no physical media — the hypervisor presents a virtual disk. Report
	// "Virtual" (honest + useful) rather than a fabricated SSD/HDD. Signatures come from the
	// disk's FriendlyName/model: VMware/Hyper-V ("... Virtual disk"), QEMU/KVM, VirtualBox.
	if strings.Contains(h, "virtual") || strings.Contains(m, "virtual") ||
		strings.Contains(h, "vmware") || strings.Contains(m, "vmware") ||
		strings.Contains(h, "qemu") || strings.Contains(m, "qemu") ||
		strings.Contains(m, "vbox") {
		return "Virtual"
	}
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

// IsVirtualHost reports whether a host's system vendor/model is a hypervisor guest signature.
// Used to label a VM's disks "Virtual" when per-disk physical-media detection returns nothing —
// a guest has no real physical media, and the per-disk CIM mapping is unreliable across Windows
// versions, but the host's own SMBIOS vendor/model reliably identifies a VM. A physical host
// running a hypervisor reports its real maker (HPE/Dell) and is NOT matched here.
func IsVirtualHost(vendor, model string) bool {
	s := strings.ToLower(strings.TrimSpace(vendor + " " + model))
	if s == "" {
		return false
	}
	for _, sig := range []string{
		"vmware", "virtual machine", "virtualbox", "innotek",
		"qemu", "kvm", "xen", "parallels", "bochs",
		"openstack", "google compute engine", "amazon ec2", "nutanix ahv",
	} {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}
