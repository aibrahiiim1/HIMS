package osinv

import (
	"context"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Writer is the narrow DB surface osinv.Persist needs. *db.Queries satisfies it;
// tests use a mock. Keeping it an interface lets the pure mapping be unit-tested
// without a live database while the production path uses the real queries.
type Writer interface {
	UpsertOSInventory(ctx context.Context, arg db.UpsertOSInventoryParams) (db.OsInventory, error)
	UpsertOSDisk(ctx context.Context, arg db.UpsertOSDiskParams) error
	DeleteStaleOSDisks(ctx context.Context, arg db.DeleteStaleOSDisksParams) error
	UpsertOSNic(ctx context.Context, arg db.UpsertOSNicParams) error
	DeleteStaleOSNics(ctx context.Context, arg db.DeleteStaleOSNicsParams) error
	UpsertOSService(ctx context.Context, arg db.UpsertOSServiceParams) error
	DeleteStaleOSServices(ctx context.Context, arg db.DeleteStaleOSServicesParams) error
	UpsertOSProcess(ctx context.Context, arg db.UpsertOSProcessParams) error
	DeleteStaleOSProcesses(ctx context.Context, arg db.DeleteStaleOSProcessesParams) error
	UpsertOSSoftware(ctx context.Context, arg db.UpsertOSSoftwareParams) error
	DeleteStaleOSSoftware(ctx context.Context, arg db.DeleteStaleOSSoftwareParams) error
	UpsertOSRole(ctx context.Context, arg db.UpsertOSRoleParams) error
	DeleteStaleOSRoles(ctx context.Context, arg db.DeleteStaleOSRolesParams) error
}

// Persist writes a Report for one device, then prunes rows from the same source
// not seen this poll (the established prune-on-poll pattern). Detected roles are
// refreshed under this source only. The source is the Report.Method
// ("winrm"|"ssh"). Returns the first error encountered.
func Persist(ctx context.Context, w Writer, deviceID uuid.UUID, rep Report, poll time.Time) error {
	src := rep.Method
	if src == "" {
		src = "snmp"
	}

	if _, err := w.UpsertOSInventory(ctx, buildOSInventoryParams(deviceID, rep)); err != nil {
		return err
	}

	for _, d := range rep.Disks {
		if err := w.UpsertOSDisk(ctx, db.UpsertOSDiskParams{
			DeviceID: deviceID, Name: d.Name, Model: ptr(d.Model), Serial: ptr(d.Serial),
			Filesystem: ptr(d.Filesystem), SizeBytes: ptr64(d.SizeBytes), TotalBytes: ptr64(d.TotalBytes),
			FreeBytes: ptr64(d.FreeBytes), Health: ptr(d.Health), CollectionSource: src, LastSeenAt: poll,
		}); err != nil {
			return err
		}
	}
	if err := w.DeleteStaleOSDisks(ctx, db.DeleteStaleOSDisksParams{DeviceID: deviceID, CollectionSource: src, LastSeenAt: poll}); err != nil {
		return err
	}

	for _, n := range rep.Nics {
		if err := w.UpsertOSNic(ctx, db.UpsertOSNicParams{
			DeviceID: deviceID, Name: n.Name, Mac: ptr(n.MAC), IpAddresses: ptr(n.IPAddresses),
			Gateway: ptr(n.Gateway), DnsServers: ptr(n.DNSServers), DhcpEnabled: &n.DHCPEnabled,
			LinkSpeedMbps: ptr64(n.LinkSpeedMbps), CollectionSource: src, LastSeenAt: poll,
		}); err != nil {
			return err
		}
	}
	if err := w.DeleteStaleOSNics(ctx, db.DeleteStaleOSNicsParams{DeviceID: deviceID, CollectionSource: src, LastSeenAt: poll}); err != nil {
		return err
	}

	for _, s := range rep.Services {
		if err := w.UpsertOSService(ctx, db.UpsertOSServiceParams{
			DeviceID: deviceID, Name: s.Name, DisplayName: ptr(s.DisplayName), Status: ptr(s.Status),
			StartType: ptr(s.StartType), Account: ptr(s.Account), Description: ptr(s.Description),
			CollectionSource: src, LastSeenAt: poll,
		}); err != nil {
			return err
		}
	}
	if err := w.DeleteStaleOSServices(ctx, db.DeleteStaleOSServicesParams{DeviceID: deviceID, CollectionSource: src, LastSeenAt: poll}); err != nil {
		return err
	}

	for _, p := range rep.Processes {
		if err := w.UpsertOSProcess(ctx, db.UpsertOSProcessParams{
			DeviceID: deviceID, Pid: int32(p.PID), Name: p.Name, CpuPct: ptrF(p.CPUPct),
			MemBytes: ptr64(p.MemBytes), StartTime: parseTimePtr(p.StartTime), CollectionSource: src, LastSeenAt: poll,
		}); err != nil {
			return err
		}
	}
	if err := w.DeleteStaleOSProcesses(ctx, db.DeleteStaleOSProcessesParams{DeviceID: deviceID, CollectionSource: src, LastSeenAt: poll}); err != nil {
		return err
	}

	for _, sw := range rep.Software {
		if err := w.UpsertOSSoftware(ctx, db.UpsertOSSoftwareParams{
			DeviceID: deviceID, Name: sw.Name, Version: sw.Version, Publisher: ptr(sw.Publisher),
			Arch: ptr(sw.Arch), InstallDate: ptr(sw.InstallDate), CollectionSource: src, LastSeenAt: poll,
		}); err != nil {
			return err
		}
	}
	if err := w.DeleteStaleOSSoftware(ctx, db.DeleteStaleOSSoftwareParams{DeviceID: deviceID, CollectionSource: src, LastSeenAt: poll}); err != nil {
		return err
	}

	// Refresh OS-detected roles (free-form, from services/packages evidence).
	roles := DetectRoles(rep)
	for _, role := range roles {
		if err := w.UpsertOSRole(ctx, db.UpsertOSRoleParams{DeviceID: deviceID, Role: role, CollectionSource: src, LastSeenAt: poll}); err != nil {
			return err
		}
	}
	if err := w.DeleteStaleOSRoles(ctx, db.DeleteStaleOSRolesParams{DeviceID: deviceID, CollectionSource: src, LastSeenAt: poll}); err != nil {
		return err
	}
	return nil
}

// DetectRoles dispatches to the OS-specific role detector.
func DetectRoles(rep Report) []string {
	if rep.Method == "ssh" {
		return DetectLinuxRoles(rep)
	}
	return DetectWindowsRoles(rep)
}

// buildOSInventoryParams maps a Report to the 1:1 upsert params (pure).
func buildOSInventoryParams(deviceID uuid.UUID, rep Report) db.UpsertOSInventoryParams {
	method := rep.Method
	if method == "" {
		method = "snmp"
	}
	p := db.UpsertOSInventoryParams{
		DeviceID: deviceID, CollectionMethod: method,
		Hostname: ptr(rep.Identity.Hostname), Fqdn: ptr(rep.Identity.FQDN),
		Domain: ptr(rep.Identity.Domain), Workgroup: ptr(rep.Identity.Workgroup),
		LoggedOnUser: ptr(rep.Identity.LoggedOnUser),
		AdDistinguishedName: ptr(rep.Identity.ADDistinguishedName), AdOuPath: ptr(rep.Identity.ADOUPath),
		OsCaption: ptr(rep.OS.Caption), OsVersion: ptr(rep.OS.Version), OsBuild: ptr(rep.OS.Build),
		OsEdition: ptr(rep.OS.Edition), OsArch: ptr(rep.OS.Arch), Kernel: ptr(rep.OS.Kernel),
		InstallDate: parseTimePtr(rep.OS.InstallDate), LastBoot: parseTimePtr(rep.OS.LastBoot),
		UptimeSeconds: ptr64(rep.OS.UptimeSeconds), Timezone: ptr(rep.OS.Timezone),
		Manufacturer: ptr(rep.Hardware.Manufacturer), Model: ptr(rep.Hardware.Model),
		Serial: ptr(rep.Hardware.Serial), AssetTag: ptr(rep.Hardware.AssetTag),
		BiosVersion: ptr(rep.Hardware.BIOSVersion), BiosDate: ptr(rep.Hardware.BIOSDate),
		CpuModel: ptr(rep.Hardware.CPUModel), CpuSockets: ptr32(rep.Hardware.CPUSockets),
		CpuCores: ptr32(rep.Hardware.CPUCores), RamTotalBytes: ptr64(rep.Hardware.RAMTotalBytes),
		RamSlots: ptr32(rep.Hardware.RAMSlots), SwapTotalBytes: ptr64(rep.Hardware.SwapTotalBytes),
	}
	if rep.Events != nil {
		p.EventsCritical24h = ptr32(rep.Events.Critical24h)
		p.EventsError24h = ptr32(rep.Events.Error24h)
		p.EventsWarning24h = ptr32(rep.Events.Warning24h)
		p.LastCriticalEvent = ptr(rep.Events.LastCritical)
	}
	return p
}

// --- nullable-conversion helpers (empty/zero → nil so the DB stores NULL,
// which the UI renders as "Not collected") ---

func ptr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
func ptr32(n int) *int32 {
	if n == 0 {
		return nil
	}
	v := int32(n)
	return &v
}
func ptr64(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
}
func ptrF(f float64) *float64 {
	if f == 0 {
		return nil
	}
	return &f
}

// parseTimePtr parses an ISO-8601 / RFC3339 timestamp, returning nil on empty or
// unparseable input (so a missing date is NULL, never a fabricated zero time).
func parseTimePtr(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
