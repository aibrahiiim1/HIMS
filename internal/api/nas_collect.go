package api

import (
	"context"
	"net/http"
	"time"

	"github.com/coralsearesorts/hims/internal/nas"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// NAS deep-collection over SNMP. Uses the device's BOUND SNMP community (never sprays)
// to read a QNAP appliance's physical disks, logical volumes, network interfaces, and
// live system health, then persists them for the NAS detail template. All values are
// real device data; nothing is synthesized — unread fields are stored NULL.

// collectNAS is POST /devices/{id}/collect-nas.
func (s *Server) collectNAS(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	dev, err := s.queries.GetDevice(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.snmpClientForDevice(ctx, dev, "", 8*time.Second)
	if err != nil {
		// Honest credential_required: a NAS with no bound SNMP community can't be collected over SNMP.
		http.Error(w, "NAS SNMP collection needs a bound SNMP v2c community: "+err.Error(), http.StatusPreconditionRequired)
		return
	}
	defer c.Close()

	rep := nas.CollectQNAP(ctx, c)
	poll := time.Now()
	s.persistNAS(ctx, dev.ID, rep, poll)

	// Network interfaces reuse the shared interfaces table (same source SwitchDetail reads).
	// Best-effort: the NAS summary still persists even if the IF-MIB pass finds no MAC.
	if _, ierr := s.collectSNMPInterfaces(ctx, dev, "", 8*time.Second); ierr != nil {
		// non-fatal — surface nothing to the client; the summary already succeeded
		_ = ierr
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"collected_at": poll,
		"disk_count":   len(rep.Disks),
		"volume_count": len(rep.Volumes),
		"report":       rep,
	})
}

// persistNAS writes the collected snapshot into nas_info / nas_disks / nas_volumes,
// then removes rows not seen in this pass (disks pulled, volumes deleted).
func (s *Server) persistNAS(ctx context.Context, devID uuid.UUID, rep nas.Report, poll time.Time) {
	_ = s.queries.UpsertNASInfo(ctx, db.UpsertNASInfoParams{
		DeviceID:         devID,
		Vendor:           nonEmptyStr(rep.Vendor),
		Model:            nonEmptyStr(rep.Model),
		Firmware:         nonEmptyStr(rep.Firmware),
		Serial:           nil, // SNMP does not expose a QNAP serial (QTS-API only) — honest NULL
		Hostname:         nonEmptyStr(rep.Hostname),
		CpuPct:           rep.CPUPct,
		MemTotalBytes:    nonZero64(rep.MemTotalBytes),
		MemUsedBytes:     nonZero64(rep.MemUsedBytes),
		CpuTempC:         intToI32Ptr(rep.CPUTempC),
		SysTempC:         intToI32Ptr(rep.SysTempC),
		UptimeSeconds:    nonZero64(rep.UptimeSeconds),
		DiskCount:        int32(len(rep.Disks)),
		VolumeCount:      int32(len(rep.Volumes)),
		Health:           nonEmptyStr(rep.Health),
		CollectionSource: "snmp",
		LastSeenAt:       poll,
	})
	for _, d := range rep.Disks {
		_ = s.queries.UpsertNASDisk(ctx, db.UpsertNASDiskParams{
			DeviceID:      devID,
			Slot:          int32(d.Slot),
			Vendor:        nonEmptyStr(d.Vendor),
			Model:         nonEmptyStr(d.Model),
			Serial:        nonEmptyStr(d.Serial),
			InterfaceType: nonEmptyStr(d.InterfaceType),
			CapacityBytes: nonZero64(d.CapacityBytes),
			TempC:         intToI32Ptr(d.TempC),
			Health:        nonEmptyStr(d.Health),
			LastSeenAt:    poll,
		})
	}
	_ = s.queries.DeleteStaleNASDisks(ctx, db.DeleteStaleNASDisksParams{DeviceID: devID, LastSeenAt: poll})
	for _, v := range rep.Volumes {
		_ = s.queries.UpsertNASVolume(ctx, db.UpsertNASVolumeParams{
			DeviceID:   devID,
			Idx:        int32(v.Index),
			Name:       v.Name,
			FsType:     nonEmptyStr(v.FSType),
			TotalBytes: nonZero64(v.TotalBytes),
			UsedBytes:  nonZero64(v.UsedBytes),
			LastSeenAt: poll,
		})
	}
	_ = s.queries.DeleteStaleNASVolumes(ctx, db.DeleteStaleNASVolumesParams{DeviceID: devID, LastSeenAt: poll})
}

// deviceNAS is GET /devices/{id}/nas — the persisted NAS inventory for the detail page.
func (s *Server) deviceNAS(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	info, err := s.queries.GetNASInfo(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"collected": false}) // nothing collected yet
		return
	}
	disks, _ := s.queries.ListNASDisks(ctx, id)
	vols, _ := s.queries.ListNASVolumes(ctx, id)
	writeJSON(w, http.StatusOK, map[string]any{
		"collected": true,
		"info":      info,
		"disks":     disks,
		"volumes":   vols,
	})
}

// intToI32Ptr converts an optional int to the *int32 sqlc expects (nil stays nil).
func intToI32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v)
	return &n
}

// nonZero64 returns nil for a zero value so "not measured" is stored as NULL, not 0.
func nonZero64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
