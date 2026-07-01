package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// physicalDriveDTO is one PHYSICAL drive from a server's linked BMC Redfish inventory. It is kept
// strictly separate from OS logical volumes: a Windows "HP LOGICAL VOLUME" is a RAID logical unit,
// NOT a disk — the real SSD/HDD/NVMe media lives here, behind the controller. Fields are shown only
// when Redfish exposed them (never fabricated).
type physicalDriveDTO struct {
	Bay           string `json:"bay,omitempty"`
	Model         string `json:"model,omitempty"`
	Serial        string `json:"serial,omitempty"`
	CapacityBytes int64  `json:"capacity_bytes"`
	Media         string `json:"media,omitempty"`    // SSD | HDD | NVMe (Redfish MediaType, NVMe from protocol)
	Protocol      string `json:"protocol,omitempty"` // SAS | SATA | NVMe
	RPM           string `json:"rpm,omitempty"`
	Status        string `json:"status,omitempty"`
}

type raidControllerDTO struct {
	Name      string `json:"name"`
	Model     string `json:"model,omitempty"`
	Firmware  string `json:"firmware,omitempty"`
	RaidTypes string `json:"raid_types,omitempty"`
	Status    string `json:"status,omitempty"`
}

// serverBMCDrivesDTO surfaces the physical-drive media of a server from its LINKED BMC's Redfish
// inventory. The link is chassis-serial / system-UUID evidence ONLY (identical rules to the BMC
// inventory page) — never IP proximity, name, subnet, topology, or NIC MAC. When there is no
// evidence-backed link, or the linked BMC has no Redfish drive inventory, it returns an honest,
// actionable gap describing the exact evidence still needed.
type serverBMCDrivesDTO struct {
	Linked         bool                `json:"linked"`
	BMCID          string              `json:"bmc_id,omitempty"`
	BMCIP          string              `json:"bmc_ip,omitempty"`
	BMCName        string              `json:"bmc_name,omitempty"`
	LinkEvidence   string              `json:"link_evidence,omitempty"`
	Controllers    []raidControllerDTO `json:"controllers,omitempty"`
	Drives         []physicalDriveDTO  `json:"drives"`
	MediaRollup    string              `json:"media_rollup,omitempty"`
	Source         string              `json:"source,omitempty"` // "BMC Redfish physical drive evidence"
	Gap            string              `json:"gap,omitempty"`
	EvidenceNeeded string              `json:"evidence_needed,omitempty"`
}

// deviceServerBMCDrives (GET /devices/{id}/bmc-drives) returns the physical drives of a SERVER
// (or virtual host) from its evidence-linked BMC's Redfish inventory — the only real source of
// SSD/HDD/NVMe media on a hardware-RAID server, where the OS sees only logical volumes.
func (s *Server) deviceServerBMCDrives(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}

	out := serverBMCDrivesDTO{Drives: []physicalDriveDTO{}}

	svSerial := normSerial(derefStr(dev.Serial))
	svUUID := normSerial(s.deviceUUID(ctx, id))
	if svSerial == "" && svUUID == "" {
		out.Gap = "Physical disk media is available only after this server is linked to its BMC by matching chassis serial. This server has no collected chassis serial or system UUID to match a controller."
		out.EvidenceNeeded = "Collect this server's chassis serial / system UUID (OS inventory, SNMP, or vSphere), and ensure its iLO/iDRAC exposes a matching serial/UUID over Redfish."
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Reverse of linkBMCToServer: find the BMC whose collected serial/UUID matches THIS server.
	// UUID wins over serial (same precedence as the BMC inventory page). Evidence only.
	bmcs, _ := s.queries.ListDevicesByCategory(ctx, string(domain.CatBMC))
	var linked *db.Device
	linkEvidence := ""
	for i := range bmcs {
		b := bmcs[i]
		if svUUID != "" && normSerial(s.deviceUUID(ctx, b.ID)) == svUUID {
			linked = &bmcs[i]
			linkEvidence = "system UUID match"
			break
		}
	}
	if linked == nil {
		for i := range bmcs {
			b := bmcs[i]
			bserial := normSerial(derefStr(b.Serial))
			if bserial == "" {
				if info, e := s.queries.GetBMCInfo(ctx, b.ID); e == nil {
					bserial = normSerial(derefStr(info.Serial))
				}
			}
			if svSerial != "" && bserial != "" && bserial == svSerial {
				linked = &bmcs[i]
				linkEvidence = "chassis serial match (" + strings.TrimSpace(derefStr(b.Serial)) + ")"
				break
			}
		}
	}

	if linked == nil {
		out.Gap = "No discovered iLO/iDRAC/Redfish controller matches this server's chassis serial or system UUID, so its physical drive media cannot be sourced. (Links are evidence-only — never inferred from IP, name, subnet, or topology.)"
		out.EvidenceNeeded = "Discover the server's BMC and collect its Redfish inventory; the BMC's chassis serial/UUID must match this server's (" + firstNonEmpty(strings.TrimSpace(derefStr(dev.Serial)), "no serial") + ")."
		writeJSON(w, http.StatusOK, out)
		return
	}

	out.Linked = true
	out.BMCID = linked.ID.String()
	out.BMCIP = addrStr(linked.PrimaryIp)
	out.BMCName = deviceName(*linked)
	out.LinkEvidence = linkEvidence

	rows, _ := s.queries.ListBMCComponents(ctx, linked.ID)
	medias := map[string]int{}
	for _, row := range rows {
		var d map[string]any
		if len(row.Detail) > 0 {
			_ = json.Unmarshal(row.Detail, &d)
		}
		get := func(k string) string {
			if v, ok := d[k].(string); ok {
				return v
			}
			return ""
		}
		switch row.Kind {
		case "controller":
			out.Controllers = append(out.Controllers, raidControllerDTO{
				Name: row.Name, Model: derefStr(row.Model), Firmware: get("firmware"),
				RaidTypes: get("raid_types"), Status: derefStr(row.Status),
			})
		case "drive":
			proto := get("protocol")
			media := get("media")
			// Present NVMe as the media type when the transport is NVMe (operators think in
			// SSD/HDD/NVMe); otherwise keep the Redfish MediaType (SSD/HDD).
			eff := media
			if strings.Contains(strings.ToLower(proto), "nvme") {
				eff = "NVMe"
			}
			if eff != "" {
				medias[eff]++
			}
			out.Drives = append(out.Drives, physicalDriveDTO{
				Bay: firstNonEmpty(get("location"), row.Name), Model: derefStr(row.Model), Serial: derefStr(row.Serial),
				CapacityBytes: row.CapacityBytes, Media: eff, Protocol: proto, RPM: get("rpm"), Status: derefStr(row.Status),
			})
		}
	}
	sort.SliceStable(out.Drives, func(i, j int) bool { return out.Drives[i].Bay < out.Drives[j].Bay })

	if len(out.Drives) == 0 {
		out.Gap = "This server IS linked to its BMC (" + out.BMCName + "), but no Redfish physical-drive inventory has been collected from it yet."
		out.EvidenceNeeded = "Collect Redfish on the BMC at " + out.BMCIP + " (BMC detail page -> Collect Redfish) to gather physical drive media."
		writeJSON(w, http.StatusOK, out)
		return
	}

	out.Source = "BMC Redfish physical drive evidence"
	out.MediaRollup = mediaRollup(medias)
	writeJSON(w, http.StatusOK, out)
}

// mediaRollup summarizes a media count map as "8 HDD · 2 SSD" (NVMe, SSD, HDD order).
func mediaRollup(counts map[string]int) string {
	parts := []string{}
	for _, k := range []string{"NVMe", "SSD", "HDD"} {
		if counts[k] > 0 {
			parts = append(parts, strconv.Itoa(counts[k])+" "+k)
		}
	}
	return strings.Join(parts, " · ")
}
