package api

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/vim25/soap"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/coralsearesorts/hims/internal/vsphere"
)

// Deep VMware/ESXi onboarding (Stage B). Tries VMware credentials (vendor_api /
// http_basic) against a host's vSphere API, and on success binds the credential,
// collects host facts + VM list + datastores, classifies the device as a
// virtual_host (esxi), and records every attempt to credential-test history.
// Secrets are decrypted in-memory only; never stored or logged.

type vsphereResult struct {
	Status         string // collected | failed
	Reason         string
	Detail         string
	CredentialUsed string
	Hosts          int
	VMs            int
	Datastores     int
}

func (r vsphereResult) ok() bool { return r.Status == "collected" }

// runVSphereCollection authenticates + collects an ESXi/vCenter host. Best-effort.
func (s *Server) runVSphereCollection(ctx context.Context, d db.Device) vsphereResult {
	res := vsphereResult{Status: "failed"}
	if d.PrimaryIp == nil || !d.PrimaryIp.IsValid() {
		res.Reason, res.Detail = "no_ip", "device has no IP to collect from"
		return res
	}
	ip := d.PrimaryIp.String()
	cph := s.cipher()
	if cph == nil {
		res.Reason, res.Detail = "encryption_unavailable", "encryption key not loaded; cannot decrypt credentials"
		return res
	}

	// Candidate credentials: VMware credentials are stored as vendor_api or
	// http_basic. Try the device's bound credential first, then others.
	type cc struct {
		id         uuid.UUID
		name       string
		user, pass string
	}
	const maxVSphereCands = 8
	// VMware/ESXi accepts a user:password over its SOAP API; on ESXi the root
	// account is the SAME credential used for SSH, so ssh-kind creds are valid
	// vSphere logins too (the operator commonly stores root/<pw> as an "ssh"
	// credential). Accept vendor_api / http_basic / ssh; the kind only filters
	// the candidate set, the actual auth is plain user:password either way.
	vsphereKind := func(k string) bool {
		return k == string(domain.CredVendorAPI) || k == string(domain.CredHTTPBasic) || k == string(domain.CredSSH)
	}
	var cands []cc
	seen := map[uuid.UUID]bool{}
	add := func(c db.Credential) {
		if seen[c.ID] || len(cands) >= maxVSphereCands || !vsphereKind(c.Kind) {
			return
		}
		seen[c.ID] = true
		plain, err := cph.Open(c.EncryptedBlob, c.KeyID)
		if err != nil {
			return
		}
		u, p := credtest.SplitUserPass(string(plain))
		cands = append(cands, cc{c.ID, c.Name, u, p})
	}
	if d.CredentialID != nil {
		if c, err := s.queries.GetCredential(ctx, *d.CredentialID); err == nil {
			add(c)
		}
	}
	if all, err := s.queries.ListCredentials(ctx); err == nil {
		for _, c := range all {
			add(c)
		}
	}
	if len(cands) == 0 {
		res.Reason, res.Detail = "no_credential", "no usable VMware credential (kind vendor_api or http_basic) — add one"
		return res
	}

	var attempts []discovery.CredAttempt
	lastReason, lastDetail := "auth_failed", "vSphere login rejected"
	for _, cd := range cands {
		inv, ver, model, vendor, err := vsphereLoginCollect(ctx, ip, cd.user, cd.pass)
		category, detail := "success", "vSphere authenticated"
		if err != nil {
			category, detail = categorizeCollectErr("vsphere", err.Error())
		}
		attempts = append(attempts, discovery.CredAttempt{
			CredentialID: cd.id, Kind: domain.CredVendorAPI, Protocol: "vmware",
			Success: err == nil, Category: category, Detail: detail,
		})
		if err != nil {
			lastReason, lastDetail = category, detail
			continue
		}

		// Success — persist host facts, VM list, classification, bind, mark up.
		hostName := ""
		if len(inv.Hosts) > 0 {
			hostName = inv.Hosts[0].Name
		}
		_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
			ID: d.ID, Vendor: vendor, Model: model, OsVersion: ver, Hostname: hostName,
		})
		if blob, merr := domain.MarshalEvidence(nil); merr == nil {
			dc := "esxi"
			conf := int16(90)
			_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
				ID: d.ID, Category: string(domain.CatVirtualHost), OsFamily: "",
				DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
			})
		}
		s.markVirtualHost(ctx, d.ID, "esxi")
		for _, vm := range inv.VMs {
			var vcpu, mem *int32
			if vm.NumCPU > 0 {
				v := vm.NumCPU
				vcpu = &v
			}
			if vm.MemoryMB > 0 {
				m := vm.MemoryMB
				mem = &m
			}
			var gos *string
			if vm.GuestOS != "" {
				g := vm.GuestOS
				gos = &g
			}
			var vmIP *netip.Addr
			if a, perr := netip.ParseAddr(vm.IP); perr == nil {
				vmIP = &a
			}
			_, _ = s.queries.UpsertVM(ctx, db.UpsertVMParams{
				HostDeviceID: d.ID, Name: vm.Name, PowerState: vm.PowerState,
				Vcpu: vcpu, MemMb: mem, GuestOs: gos, PrimaryIp: vmIP,
				VmDeviceID: s.resolveVMDevice(ctx, vmIP, ""),
			})
		}
		cid := cd.id
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: &cid})
		_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})

		res = vsphereResult{
			Status: "collected", CredentialUsed: cd.name,
			Hosts: len(inv.Hosts), VMs: len(inv.VMs), Datastores: len(inv.Datastores),
			Detail: "collected via vSphere using credential " + cd.name,
		}
		s.persistScanCredAttempts(ctx, d, attempts, "default")
		return res
	}
	s.persistScanCredAttempts(ctx, d, attempts, "default")
	res.Reason, res.Detail = lastReason, lastDetail
	return res
}

// profileCollect is the structured outcome of a profile-driven deep collection.
// AuthOK distinguishes "test/login succeeded" from "collection succeeded" so the
// Scan Results UI can show test-vs-collection state independently.
type profileCollect struct {
	AuthOK       bool
	CollectionOK bool
	Detail       string
	Category     string // refined category (virtual_host / camera / nvr)
}

// collectVSphereProfile onboards an ESXi/vCenter host using a Vendor Connection
// Profile: it authenticates to the PROFILE's target URL with the profile's bound
// credential (not just the scanned device IP), collects host + VM + datastore
// facts, classifies as esxi/vcenter, binds the credential on success, and records
// the attempt to Credential Test History. No secrets are logged. Binds only on
// authenticated success.
func (s *Server) collectVSphereProfile(ctx context.Context, p db.VendorConnectionProfile, d db.Device) profileCollect {
	out := profileCollect{Category: string(domain.CatVirtualHost)}
	user, pass, credID, kind, ok := s.vendorProfileCred(ctx, p)
	if !ok {
		out.Detail = "no usable credential bound to this profile (or encryption key not loaded)"
		return out
	}
	sdk := vsphereSDKURL(strings.TrimRight(p.TargetUrl, "/"))
	inv, ver, model, vendor, err := vsphereLoginCollectURL(ctx, sdk, user, pass)
	category, detail := "success", "vSphere authenticated"
	if err != nil {
		category, detail = categorizeCollectErr("vsphere", err.Error())
	}
	s.persistScanCredAttempts(ctx, d, []discovery.CredAttempt{{
		CredentialID: credID, Kind: kind, Protocol: "vmware",
		Success: err == nil, Category: category, Detail: detail,
	}}, "default")
	if err != nil {
		out.Detail = "vSphere login failed: " + detail
		return out
	}
	out.AuthOK = true

	// vCenter manages multiple hosts; a standalone ESXi reports one. Classify
	// accordingly (honest heuristic from authenticated evidence).
	hostName, deviceClass := "", "esxi"
	if len(inv.Hosts) > 0 {
		hostName = inv.Hosts[0].Name
	}
	if len(inv.Hosts) > 1 {
		deviceClass = "vcenter"
	}
	_ = s.queries.UpdateDeviceHardwareInfo(ctx, db.UpdateDeviceHardwareInfoParams{
		ID: d.ID, Vendor: vendor, Model: model, OsVersion: ver, Hostname: hostName,
	})
	if blob, merr := domain.MarshalEvidence(nil); merr == nil {
		conf := int16(92)
		_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
			ID: d.ID, Category: string(domain.CatVirtualHost), OsFamily: "",
			DeviceClass: &deviceClass, ConfidenceScore: &conf, ClassificationEvidence: blob,
		})
	}
	s.markVirtualHost(ctx, d.ID, "esxi")
	for _, vm := range inv.VMs {
		var vcpu, mem *int32
		if vm.NumCPU > 0 {
			v := vm.NumCPU
			vcpu = &v
		}
		if vm.MemoryMB > 0 {
			m := vm.MemoryMB
			mem = &m
		}
		var gos *string
		if vm.GuestOS != "" {
			g := vm.GuestOS
			gos = &g
		}
		var vmIP *netip.Addr
		if a, perr := netip.ParseAddr(vm.IP); perr == nil {
			vmIP = &a
		}
		_, _ = s.queries.UpsertVM(ctx, db.UpsertVMParams{
			HostDeviceID: d.ID, Name: vm.Name, PowerState: vm.PowerState,
			Vcpu: vcpu, MemMb: mem, GuestOs: gos, PrimaryIp: vmIP,
			VmDeviceID: s.resolveVMDevice(ctx, vmIP, ""),
		})
	}
	if p.CredentialID != nil {
		_ = s.queries.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{ID: d.ID, CredentialID: p.CredentialID})
	}
	_ = s.queries.UpdateDeviceMonitoringStatus(ctx, db.UpdateDeviceMonitoringStatusParams{ID: d.ID, Status: "up"})

	out.CollectionOK = true
	out.Detail = deviceClass + " collected via profile " + p.Name + " — " +
		itoaN(len(inv.Hosts)) + " host(s), " + itoaN(len(inv.VMs)) + " VM(s), " + itoaN(len(inv.Datastores)) + " datastore(s)"
	return out
}

// vsphereLoginCollect logs into a vSphere/ESXi endpoint and collects inventory.
// The session is always logged out. InsecureSkipVerify because ESXi ships a
// self-signed certificate.
func vsphereLoginCollect(ctx context.Context, ip, user, pass string) (vsphere.Inventory, string, string, string, error) {
	return vsphereLoginCollectURL(ctx, "https://"+ip+"/sdk", user, pass)
}

// vsphereLoginCollectURL is vsphereLoginCollect against an explicit SDK URL (used
// by profile-driven collection, where the target may differ from the device IP).
func vsphereLoginCollectURL(ctx context.Context, sdkURL, user, pass string) (vsphere.Inventory, string, string, string, error) {
	var inv vsphere.Inventory
	u, err := soap.ParseURL(sdkURL)
	if err != nil {
		return inv, "", "", "", err
	}
	u.User = url.UserPassword(user, pass)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c, err := govmomi.NewClient(cctx, u, true)
	if err != nil {
		return inv, "", "", "", err
	}
	defer func() { _ = c.Logout(context.Background()) }()

	inv, err = vsphere.Collect(cctx, c.Client)
	if err != nil {
		return inv, "", "", "", err
	}
	ver, model, vendor := "", "", ""
	if len(inv.Hosts) > 0 {
		h := inv.Hosts[0]
		ver, model, vendor = h.FullName, h.Model, h.Vendor
		if ver == "" {
			ver = h.Version
		}
	}
	if vendor == "" {
		vendor = "VMware"
	}
	return inv, ver, model, vendor, nil
}

// collectVSphere handles POST /devices/{id}/collect-vsphere — operator-triggered
// VMware onboarding for an ESXi/vCenter host.
func (s *Server) collectVSphere(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	d, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	res := s.runVSphereCollection(ctx, d)
	if res.ok() {
		s.audit(r, "inventory", "device.collect_vsphere", "device", id.String(),
			"Collected VMware facts for "+d.Name, map[string]any{"hosts": res.Hosts, "vms": res.VMs, "datastores": res.Datastores})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"collected": res.ok(), "reason": res.Reason, "detail": res.Detail,
		"credential_used": res.CredentialUsed, "hosts": res.Hosts, "vms": res.VMs, "datastores": res.Datastores,
	})
}

// resolveVMDevice links a hosted VM to an EXISTING discovered device when the VM's
// guest IP already belongs to one — so a VM is never duplicated as a second fake device
// and the VM↔device cross-link works both ways (host→VM→device, device→its VM record).
// It deliberately does NOT link to a virtual_host (a hypervisor is not its own guest) and
// never invents a device: an unmatched VM stays a host-child VM record with vm_device_id NULL.
// It matches by guest IP first, then by vNIC MAC (the reliable key for Hyper-V guests with
// no integration-services IP). mac may be a comma-joined list ("" if none).
func (s *Server) resolveVMDevice(ctx context.Context, vmIP *netip.Addr, mac string) *uuid.UUID {
	if vmIP != nil {
		if dev, err := s.queries.LiveDeviceByIP(ctx, vmIP); err == nil && !dev.IsVirtual && dev.Category != string(domain.CatVirtualHost) {
			id := dev.ID
			return &id
		}
	}
	for _, m := range strings.Split(mac, ",") {
		if m = strings.TrimSpace(m); m == "" {
			continue
		}
		if id, err := s.queries.DeviceIDByMAC(ctx, m); err == nil && id != uuid.Nil {
			return &id
		}
	}
	return nil
}

// markVirtualHost stamps the hypervisor ROLE (esxi_host / hyperv_host) + a consistent
// hypervisor.type FACT on a collected host, so the read model derives a reliable server_role
// (virtual_host_esxi / virtual_host_hyperv) regardless of which collection path ran. Both
// writes are idempotent and best-effort.
func (s *Server) markVirtualHost(ctx context.Context, id uuid.UUID, hvType string) {
	role := string(domain.RoleESXiHost)
	if hvType == "hyperv" {
		role = string(domain.RoleHyperVHost)
	}
	_ = s.queries.AddDeviceRole(ctx, db.AddDeviceRoleParams{DeviceID: id, Role: role, Source: hvType})
	hv := hvType
	_ = s.queries.UpsertDeviceFact(ctx, db.UpsertDeviceFactParams{DeviceID: id, Key: "hypervisor.type", Value: &hv, Driver: hvType})
}

// reportIsHyperV reports whether a collected Windows host is a Hyper-V hypervisor. The
// RELIABLE signal is the Hyper-V Virtual Machine Management service (vmms) running — present
// on every Hyper-V host regardless of whether the Hyper-V PowerShell module (Get-VM) is
// installed (Server Core / role-without-tools hosts have vmms but no Get-VM). Enumerated VMs
// also imply it. This is what makes Hyper-V host DETECTION robust even when VM enumeration
// (Get-VM) returns nothing.
func reportIsHyperV(rep osinv.Report) bool {
	for _, sv := range rep.Services {
		if strings.EqualFold(sv.Name, "vmms") && strings.EqualFold(sv.Status, "Running") {
			return true
		}
	}
	return len(rep.VMs) > 0
}

// markHyperVHost classifies a Windows host that reported Hyper-V guests in-band as a
// virtual_host + hyperv_host (called AFTER the OS reclassify so the hypervisor role is not
// overwritten by the plain-Windows caption), and persists each guest VM linked to an existing
// discovered device by guest IP — so Hyper-V hosts and their VMs appear exactly like ESXi ones,
// with no duplicate fake VM devices.
func (s *Server) markHyperVHost(ctx context.Context, id uuid.UUID, vms []osinv.ReportVM) {
	if blob, err := domain.MarshalEvidence(nil); err == nil {
		dc := "hyperv"
		conf := int16(88)
		_, _ = s.queries.UpdateDeviceClassification(ctx, db.UpdateDeviceClassificationParams{
			ID: id, Category: string(domain.CatVirtualHost), OsFamily: domain.OSFamilyWindows,
			DeviceClass: &dc, ConfidenceScore: &conf, ClassificationEvidence: blob,
		})
	}
	s.markVirtualHost(ctx, id, "hyperv")
	for _, vm := range vms {
		var vcpu, mem *int32
		if vm.VCPU > 0 {
			v := vm.VCPU
			vcpu = &v
		}
		if vm.MemoryMB > 0 {
			m := vm.MemoryMB
			mem = &m
		}
		var gos *string
		if vm.GuestOS != "" {
			g := vm.GuestOS
			gos = &g
		}
		var vmIP *netip.Addr
		if first := strings.SplitN(vm.IP, ",", 2)[0]; first != "" {
			if a, perr := netip.ParseAddr(strings.TrimSpace(first)); perr == nil {
				vmIP = &a
			}
		}
		ps := vm.PowerState
		switch ps {
		case "on", "off", "suspended":
		default:
			ps = "unknown"
		}
		var vmID, mac *string
		if vm.VMID != "" {
			v := vm.VMID
			vmID = &v
		}
		if vm.MAC != "" {
			m := vm.MAC
			mac = &m
		}
		_, _ = s.queries.UpsertVM(ctx, db.UpsertVMParams{
			HostDeviceID: id, Name: vm.Name, PowerState: ps,
			Vcpu: vcpu, MemMb: mem, GuestOs: gos, PrimaryIp: vmIP,
			VmDeviceID: s.resolveVMDevice(ctx, vmIP, vm.MAC), VmID: vmID, Mac: mac,
		})
	}
}
