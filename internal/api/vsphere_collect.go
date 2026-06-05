package api

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/vim25/soap"

	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
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
	const maxVSphereCands = 6
	var cands []cc
	seen := map[uuid.UUID]bool{}
	add := func(c db.Credential) {
		if seen[c.ID] || len(cands) >= maxVSphereCands || (c.Kind != string(domain.CredVendorAPI) && c.Kind != string(domain.CredHTTPBasic)) {
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
		s.persistScanCredAttempts(ctx, d, attempts)
		return res
	}
	s.persistScanCredAttempts(ctx, d, attempts)
	res.Reason, res.Detail = lastReason, lastDetail
	return res
}

// vsphereLoginCollect logs into a vSphere/ESXi endpoint and collects inventory.
// The session is always logged out. InsecureSkipVerify because ESXi ships a
// self-signed certificate.
func vsphereLoginCollect(ctx context.Context, ip, user, pass string) (vsphere.Inventory, string, string, string, error) {
	var inv vsphere.Inventory
	u, err := soap.ParseURL("https://" + ip + "/sdk")
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
