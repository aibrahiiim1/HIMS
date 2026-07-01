package redfish

import (
	"context"
	"fmt"
	"strings"
)

// BMCFacts is the normalized out-of-band inventory + health for one server.
type BMCFacts struct {
	Vendor          string // HPE | Dell | (raw manufacturer)
	ControllerKind  string // iLO | iDRAC | redfish
	Model           string
	Serial          string
	SKU             string
	BiosVersion     string
	FirmwareVersion string // iLO/iDRAC firmware
	PowerState      string // On | Off | ...
	Health          string // OK | Warning | Critical
	ProcessorCount  int
	ProcessorModel  string
	ProcessorCores  int
	MemoryGiB       float64
	Sensors         []Sensor
	Components      []Component // detailed CPU / DIMM / RAID controller / volume / drive inventory
}

// Component is one detailed hardware item collected over authenticated Redfish: a
// processor, a memory module, a storage (RAID) controller, a volume (RAID array), or a
// physical drive. Detail carries the kind-specific fields (cores/speed, media/protocol,
// raid level, firmware…) as strings so the model stays flat and honest — only what the
// device reported is present.
type Component struct {
	Kind          string            // cpu | memory | controller | volume | drive
	Name          string            // socket/locator/name (stable within kind)
	Model         string            //
	Serial        string            //
	Status        string            // OK | Warning | Critical | (raw)
	CapacityBytes int64             // DIMM/drive/volume capacity; 0 for CPU
	Detail        map[string]string //
}

// Sensor is one fan / PSU / temperature / drive-or-storage health reading.
type Sensor struct {
	Kind       string // fan | psu | temperature | storage
	Name       string
	Status     string // OK | Warning | Critical | (raw)
	Reading    float64
	Unit       string
	HasReading bool
}

// --- partial Redfish schema (only the fields we read) -----------------------

type odataRef struct {
	ID   string `json:"@odata.id"` // Redfish 1.2+ (iLO 5 / iDRAC)
	Href string `json:"href"`      // Redfish 1.0.0 (iLO 4) legacy link form
}

// id returns the resource path regardless of Redfish generation.
func (o odataRef) id() string {
	if o.ID != "" {
		return o.ID
	}
	return o.Href
}

type status struct {
	Health string `json:"Health"`
	State  string `json:"State"`
}
type serviceRoot struct {
	Vendor   string              `json:"Vendor"`
	Product  string              `json:"Product"`
	Systems  odataRef            `json:"Systems"`
	Chassis  odataRef            `json:"Chassis"`
	Managers odataRef            `json:"Managers"`
	Oem      map[string]struct{} `json:"Oem"`
}
type collection struct {
	Members []odataRef `json:"Members"`
}
type computerSystem struct {
	Manufacturer string `json:"Manufacturer"`
	Model        string `json:"Model"`
	SKU          string `json:"SKU"`
	SerialNumber string `json:"SerialNumber"`
	BiosVersion  string `json:"BiosVersion"`
	PowerState   string `json:"PowerState"`
	Status       status `json:"Status"`
	ProcessorSum struct {
		Count int    `json:"Count"`
		Model string `json:"Model"`
	} `json:"ProcessorSummary"`
	MemorySum struct {
		TotalSystemMemoryGiB float64 `json:"TotalSystemMemoryGiB"`
	} `json:"MemorySummary"`
	Processors         odataRef `json:"Processors"`
	Memory             odataRef `json:"Memory"`
	Storage            odataRef `json:"Storage"`
	EthernetInterfaces odataRef `json:"EthernetInterfaces"`
	Oem                struct {
		Hpe struct { // iLO 5 (Redfish 1.2+)
			Links struct {
				SmartStorage odataRef `json:"SmartStorage"`
			} `json:"Links"`
		} `json:"Hpe"`
		Hp struct { // iLO 4 (Redfish 1.0.0) — detailed resources hang off Oem.Hp.links
			Links struct {
				SmartStorage       odataRef `json:"SmartStorage"`
				Memory             odataRef `json:"Memory"`
				NetworkAdapters    odataRef `json:"NetworkAdapters"`
				EthernetInterfaces odataRef `json:"EthernetInterfaces"`
			} `json:"links"`
		} `json:"Hp"`
	} `json:"Oem"`
}

// ethernetInterface is one NIC (host or management), across Redfish generations.
type ethernetInterface struct {
	Name             string `json:"Name"`
	MACAddress       string `json:"MACAddress"`
	PermanentMAC     string `json:"PermanentMACAddress"`
	SpeedMbps        int    `json:"SpeedMbps"`
	FullDuplex       *bool  `json:"FullDuplex"`
	LinkStatus       string `json:"LinkStatus"` // LinkUp | LinkDown | NoLink
	InterfaceEnabled *bool  `json:"InterfaceEnabled"`
	Status           status `json:"Status"`
	IPv4Addresses    []struct {
		Address string `json:"Address"`
	} `json:"IPv4Addresses"`
}

// --- detailed inventory schema (only the fields we read) --------------------

type processor struct {
	Name           string `json:"Name"`
	Socket         string `json:"Socket"`
	Model          string `json:"Model"`
	Manufacturer   string `json:"Manufacturer"`
	ProcessorType  string `json:"ProcessorType"`
	InstructionSet string `json:"InstructionSet"`
	TotalCores     int    `json:"TotalCores"`
	TotalThreads   int    `json:"TotalThreads"`
	MaxSpeedMHz    int    `json:"MaxSpeedMHz"`
	Status         status `json:"Status"`
}
type memoryModule struct {
	Name              string `json:"Name"`
	DeviceLocator     string `json:"DeviceLocator"`    // iLO5 / iDRAC
	SocketLocator     string `json:"SocketLocator"`    // iLO4
	MemoryDeviceType  string `json:"MemoryDeviceType"` // iLO5 / iDRAC (DDR4)
	DIMMType          string `json:"DIMMType"`         // iLO4 (DDR3)
	Manufacturer      string `json:"Manufacturer"`
	PartNumber        string `json:"PartNumber"`
	SerialNumber      string `json:"SerialNumber"`
	CapacityMiB       int64  `json:"CapacityMiB"`         // iLO5 / iDRAC
	SizeMB            int64  `json:"SizeMB"`              // iLO4
	OperatingSpeedMhz int    `json:"OperatingSpeedMhz"`   // iLO5 / iDRAC
	MaxFreqMHz        int    `json:"MaximumFrequencyMHz"` // iLO4
	DIMMStatus        string `json:"DIMMStatus"`          // iLO4 (GoodInUse)
	Status            status `json:"Status"`
}
type storageDetail struct {
	Name               string `json:"Name"`
	StorageControllers []struct {
		Name               string   `json:"Name"`
		Model              string   `json:"Model"`
		Manufacturer       string   `json:"Manufacturer"`
		FirmwareVersion    string   `json:"FirmwareVersion"`
		SupportedRAIDTypes []string `json:"SupportedRAIDTypes"`
		Status             status   `json:"Status"`
	} `json:"StorageControllers"`
	Drives  []odataRef `json:"Drives"`
	Volumes odataRef   `json:"Volumes"`
}
type driveDetail struct {
	Name             string  `json:"Name"`
	Model            string  `json:"Model"`
	Manufacturer     string  `json:"Manufacturer"`
	SerialNumber     string  `json:"SerialNumber"`
	MediaType        string  `json:"MediaType"` // HDD | SSD
	Protocol         string  `json:"Protocol"`  // SAS | SATA | NVMe
	CapacityBytes    int64   `json:"CapacityBytes"`
	RotationSpeedRPM float64 `json:"RotationSpeedRPM"`
	Status           status  `json:"Status"`
}
type volumeDetail struct {
	Name          string `json:"Name"`
	RAIDType      string `json:"RAIDType"`   // RAID0 | RAID1 | RAID5 | …
	VolumeType    string `json:"VolumeType"` // legacy field on some BMCs
	CapacityBytes int64  `json:"CapacityBytes"`
	Status        status `json:"Status"`
}
type chassis struct {
	Thermal odataRef `json:"Thermal"`
	Power   odataRef `json:"Power"`
}
type thermal struct {
	Temperatures []struct {
		Name           string  `json:"Name"`
		ReadingCelsius float64 `json:"ReadingCelsius"`
		Status         status  `json:"Status"`
	} `json:"Temperatures"`
	Fans []struct {
		Name    string  `json:"Name"`
		Reading float64 `json:"Reading"`
		Units   string  `json:"ReadingUnits"`
		Status  status  `json:"Status"`
	} `json:"Fans"`
}
type power struct {
	PowerSupplies []struct {
		Name             string  `json:"Name"`
		LineInputVoltage float64 `json:"LineInputVoltage"`
		LastPowerOutputW float64 `json:"LastPowerOutputWatts"`
		Status           status  `json:"Status"`
	} `json:"PowerSupplies"`
}
type manager struct {
	Model              string   `json:"Model"`
	FirmwareVersion    string   `json:"FirmwareVersion"`
	EthernetInterfaces odataRef `json:"EthernetInterfaces"`
	Links              struct {
		EthernetNICs odataRef `json:"EthernetNICs"` // iLO4 legacy path to the mgmt NIC
	} `json:"links"`
}

// Collect walks the Redfish tree and assembles BMCFacts. Optional sections
// (thermal/power/storage) are best-effort — a fetch error on one leaves that
// data empty rather than failing the whole collection.
func Collect(ctx context.Context, c *Client) (BMCFacts, error) {
	var root serviceRoot
	if err := c.GetJSON(ctx, "/redfish/v1/", &root); err != nil {
		return BMCFacts{}, err
	}
	f := BMCFacts{Vendor: root.Vendor, ControllerKind: "redfish"}
	for k := range root.Oem {
		switch strings.ToLower(k) {
		case "hpe", "hp":
			f.Vendor, f.ControllerKind = "HPE", "iLO"
		case "dell":
			f.Vendor, f.ControllerKind = "Dell", "iDRAC"
		}
	}

	// ComputerSystem (first member).
	if sysPath := firstMember(ctx, c, root.Systems.id()); sysPath != "" {
		var sys computerSystem
		if err := c.GetJSON(ctx, sysPath, &sys); err == nil {
			if f.Vendor == "" {
				f.Vendor = sys.Manufacturer
			}
			f.Model, f.SKU, f.Serial = sys.Model, sys.SKU, sys.SerialNumber
			f.BiosVersion, f.PowerState, f.Health = sys.BiosVersion, sys.PowerState, sys.Status.Health
			f.ProcessorCount, f.ProcessorModel = sys.ProcessorSum.Count, sys.ProcessorSum.Model
			f.MemoryGiB = sys.MemorySum.TotalSystemMemoryGiB
			// Detailed inventory (best-effort). iLO 4 (Redfish 1.0.0) hangs the detailed
			// resources off Oem.Hp.links with legacy href refs instead of the standard
			// Systems/{Processors,Memory,Storage} — fall back to those so Gen8 iLOs get
			// memory/storage/NIC detail too, not just the summary.
			f.collectProcessors(ctx, c, sys.Processors.id())
			f.collectMemory(ctx, c, firstNonEmpty(sys.Memory.id(), sys.Oem.Hp.Links.Memory.id()))
			f.collectStorage(ctx, c, sys.Storage.id())
			if countComponentKind(f.Components, "drive") == 0 {
				f.collectHPESmartStorage(ctx, c, firstNonEmpty(sys.Oem.Hpe.Links.SmartStorage.id(), sys.Oem.Hp.Links.SmartStorage.id()))
			}
			// Physical/host network interfaces (MAC, link, speed).
			f.collectNICs(ctx, c, firstNonEmpty(sys.EthernetInterfaces.id(), sys.Oem.Hp.Links.EthernetInterfaces.id()), "host")
			// iLO 4 exposes no per-socket Processors collection — surface the real CPU
			// summary (count + model) as rows so the processor data isn't blank.
			if countComponentKind(f.Components, "cpu") == 0 && sys.ProcessorSum.Count > 0 && sys.ProcessorSum.Model != "" {
				for i := 0; i < sys.ProcessorSum.Count; i++ {
					f.Components = append(f.Components, Component{Kind: "cpu", Name: fmt.Sprintf("CPU %d", i+1),
						Model: strings.TrimSpace(sys.ProcessorSum.Model), Status: "OK", Detail: map[string]string{"source": "summary"}})
				}
			}
		}
	}

	// Chassis thermal + power (first member).
	if chPath := firstMember(ctx, c, root.Chassis.id()); chPath != "" {
		var ch chassis
		if err := c.GetJSON(ctx, chPath, &ch); err == nil {
			f.collectThermal(ctx, c, ch.Thermal.id())
			f.collectPower(ctx, c, ch.Power.id())
		}
	}

	// Manager firmware (iLO/iDRAC version).
	if mgrPath := firstMember(ctx, c, root.Managers.id()); mgrPath != "" {
		var mgr manager
		if err := c.GetJSON(ctx, mgrPath, &mgr); err == nil {
			f.FirmwareVersion = mgr.FirmwareVersion
			if f.ControllerKind == "redfish" && mgr.Model != "" {
				f.ControllerKind = mgr.Model
			}
			// The controller's OWN management NIC (MAC/IP/speed) — always populated, unlike
			// the sparse host NICs on older iLO.
			f.collectNICs(ctx, c, firstNonEmpty(mgr.EthernetInterfaces.id(), mgr.Links.EthernetNICs.id()), "management")
		}
	}
	return f, nil
}

func firstMember(ctx context.Context, c *Client, collPath string) string {
	if collPath == "" {
		return ""
	}
	var col collection
	if err := c.GetJSON(ctx, collPath, &col); err != nil || len(col.Members) == 0 {
		return ""
	}
	return col.Members[0].id()
}

func (f *BMCFacts) collectThermal(ctx context.Context, c *Client, path string) {
	if path == "" {
		return
	}
	var t thermal
	if err := c.GetJSON(ctx, path, &t); err != nil {
		return
	}
	for _, temp := range t.Temperatures {
		f.Sensors = append(f.Sensors, Sensor{
			Kind: "temperature", Name: temp.Name, Status: temp.Status.Health,
			Reading: temp.ReadingCelsius, Unit: "C", HasReading: true,
		})
	}
	for _, fan := range t.Fans {
		f.Sensors = append(f.Sensors, Sensor{
			Kind: "fan", Name: fan.Name, Status: fan.Status.Health,
			Reading: fan.Reading, Unit: orDefault(fan.Units, "RPM"), HasReading: true,
		})
	}
}

func (f *BMCFacts) collectPower(ctx context.Context, c *Client, path string) {
	if path == "" {
		return
	}
	var p power
	if err := c.GetJSON(ctx, path, &p); err != nil {
		return
	}
	for _, ps := range p.PowerSupplies {
		f.Sensors = append(f.Sensors, Sensor{
			Kind: "psu", Name: ps.Name, Status: ps.Status.Health,
			Reading: ps.LastPowerOutputW, Unit: "W", HasReading: ps.LastPowerOutputW > 0,
		})
	}
}

// collectProcessors reads each populated CPU socket → a cpu Component + the CPU summary
// (model + count + cores) used by the overview.
func (f *BMCFacts) collectProcessors(ctx context.Context, c *Client, path string) {
	if path == "" {
		return
	}
	for _, m := range membersOf(ctx, c, path) {
		var p processor
		if err := c.GetJSON(ctx, m, &p); err != nil {
			continue
		}
		if p.Model == "" && p.TotalCores == 0 { // empty/absent socket
			continue
		}
		name := orDefault(orDefault(p.Socket, p.Name), "CPU")
		comp := Component{Kind: "cpu", Name: name, Model: strings.TrimSpace(p.Model), Status: p.Status.Health, Detail: map[string]string{}}
		putInt(comp.Detail, "cores", p.TotalCores)
		putInt(comp.Detail, "threads", p.TotalThreads)
		putInt(comp.Detail, "max_speed_mhz", p.MaxSpeedMHz)
		putStr(comp.Detail, "manufacturer", p.Manufacturer)
		putStr(comp.Detail, "type", p.ProcessorType)
		putStr(comp.Detail, "arch", p.InstructionSet)
		f.Components = append(f.Components, comp)
		if f.ProcessorModel == "" {
			f.ProcessorModel = strings.TrimSpace(p.Model)
		}
		f.ProcessorCores += p.TotalCores
	}
	// If the summary didn't provide a count, use the sockets we actually read.
	if cnt := countComponentKind(f.Components, "cpu"); f.ProcessorCount == 0 && cnt > 0 {
		f.ProcessorCount = cnt
	}
}

// collectMemory reads each POPULATED DIMM → a memory Component; empty slots (0 MiB) are
// skipped. Fills the total memory if the MemorySummary didn't provide it.
func (f *BMCFacts) collectMemory(ctx context.Context, c *Client, path string) {
	if path == "" {
		return
	}
	var totalMiB int64
	for _, m := range membersOf(ctx, c, path) {
		var mm memoryModule
		if err := c.GetJSON(ctx, m, &mm); err != nil {
			continue
		}
		capMiB := mm.CapacityMiB // iLO5 / iDRAC
		if capMiB <= 0 {
			capMiB = mm.SizeMB // iLO4
		}
		if capMiB <= 0 { // empty slot
			continue
		}
		totalMiB += capMiB
		name := firstNonEmpty(mm.DeviceLocator, mm.SocketLocator, mm.Name, "DIMM")
		health := mm.Status.Health
		if health == "" && mm.DIMMStatus != "" { // iLO4 reports DIMMStatus (GoodInUse) not Status.Health
			health = mapDIMMStatus(mm.DIMMStatus)
		}
		comp := Component{Kind: "memory", Name: strings.TrimSpace(name), Model: strings.TrimSpace(mm.PartNumber),
			Serial: strings.TrimSpace(mm.SerialNumber), Status: health,
			CapacityBytes: capMiB * 1024 * 1024, Detail: map[string]string{}}
		putStr(comp.Detail, "type", firstNonEmpty(mm.MemoryDeviceType, mm.DIMMType))
		if sp := mm.OperatingSpeedMhz; sp > 0 {
			putInt(comp.Detail, "speed_mhz", sp)
		} else {
			putInt(comp.Detail, "speed_mhz", mm.MaxFreqMHz)
		}
		putStr(comp.Detail, "manufacturer", strings.TrimSpace(mm.Manufacturer))
		f.Components = append(f.Components, comp)
	}
	if f.MemoryGiB == 0 && totalMiB > 0 {
		f.MemoryGiB = float64(totalMiB) / 1024
	}
}

// collectNICs reads EthernetInterfaces → nic Components (MAC, link, speed, IPv4). role is
// "host" (server NICs) or "management" (the iLO/iDRAC's own NIC). NICs the controller
// exposes with no MAC and no IP (empty iLO4 host placeholders) are skipped — a NIC row
// with nothing on it is noise, not data.
func (f *BMCFacts) collectNICs(ctx context.Context, c *Client, path, role string) {
	if path == "" {
		return
	}
	for _, m := range membersOf(ctx, c, path) {
		var e ethernetInterface
		if err := c.GetJSON(ctx, m, &e); err != nil {
			continue
		}
		mac := firstNonEmpty(e.MACAddress, e.PermanentMAC)
		ip := ""
		if len(e.IPv4Addresses) > 0 {
			ip = e.IPv4Addresses[0].Address
		}
		if mac == "" && ip == "" {
			continue
		}
		name := firstNonEmpty(e.Name, mac)
		if role == "management" && e.Name != "" { // disambiguate the mgmt NIC from a host NIC of the same index
			name = e.Name
		}
		comp := Component{Kind: "nic", Name: name, Serial: mac, Status: e.Status.Health, Detail: map[string]string{}}
		putStr(comp.Detail, "mac", mac)
		putStr(comp.Detail, "link", e.LinkStatus)
		putStr(comp.Detail, "ipv4", ip)
		putStr(comp.Detail, "role", role)
		if e.SpeedMbps > 0 {
			putInt(comp.Detail, "speed_mbps", e.SpeedMbps)
		}
		f.Components = append(f.Components, comp)
	}
}

func mapDIMMStatus(s string) string {
	switch strings.ToLower(s) {
	case "goodinuse", "good", "inuse":
		return "OK"
	case "notpresent", "empty":
		return ""
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// collectStorage reads each storage subsystem → RAID controller Components, volume (RAID
// array) Components, and physical drive Components (HDD/SSD, capacity, media, protocol).
func (f *BMCFacts) collectStorage(ctx context.Context, c *Client, path string) {
	if path == "" {
		return
	}
	for _, m := range membersOf(ctx, c, path) {
		var sd storageDetail
		if err := c.GetJSON(ctx, m, &sd); err != nil {
			continue
		}
		for _, sc := range sd.StorageControllers {
			if sc.Name == "" && sc.Model == "" {
				continue
			}
			comp := Component{Kind: "controller", Name: orDefault(sc.Name, sd.Name), Model: strings.TrimSpace(sc.Model), Status: sc.Status.Health, Detail: map[string]string{}}
			putStr(comp.Detail, "firmware", sc.FirmwareVersion)
			putStr(comp.Detail, "manufacturer", sc.Manufacturer)
			if len(sc.SupportedRAIDTypes) > 0 {
				comp.Detail["raid_types"] = strings.Join(sc.SupportedRAIDTypes, ",")
			}
			f.Components = append(f.Components, comp)
		}
		// Volumes (RAID arrays).
		for _, vp := range membersOf(ctx, c, sd.Volumes.id()) {
			var v volumeDetail
			if err := c.GetJSON(ctx, vp, &v); err != nil {
				continue
			}
			comp := Component{Kind: "volume", Name: orDefault(v.Name, "Volume"), Status: v.Status.Health, CapacityBytes: v.CapacityBytes, Detail: map[string]string{}}
			putStr(comp.Detail, "raid", orDefault(v.RAIDType, v.VolumeType))
			f.Components = append(f.Components, comp)
		}
		// Physical drives.
		for _, dref := range sd.Drives {
			var d driveDetail
			if err := c.GetJSON(ctx, dref.id(), &d); err != nil {
				continue
			}
			if d.Name == "" && d.Model == "" {
				continue
			}
			comp := Component{Kind: "drive", Name: orDefault(d.Name, d.Model), Model: strings.TrimSpace(d.Model),
				Serial: strings.TrimSpace(d.SerialNumber), Status: d.Status.Health, CapacityBytes: d.CapacityBytes, Detail: map[string]string{}}
			putStr(comp.Detail, "media", d.MediaType)
			putStr(comp.Detail, "protocol", d.Protocol)
			putStr(comp.Detail, "manufacturer", d.Manufacturer)
			if d.RotationSpeedRPM > 0 {
				putInt(comp.Detail, "rpm", int(d.RotationSpeedRPM))
			}
			f.Components = append(f.Components, comp)
		}
	}
}

// --- HPE OEM SmartStorage (iLO): arrays, logical drives, physical disks ------

type hpeSmartStorage struct {
	Links struct {
		ArrayControllers odataRef `json:"ArrayControllers"`
	} `json:"Links"`
}
type hpeArrayController struct {
	Model           string `json:"Model"`
	SerialNumber    string `json:"SerialNumber"`
	FirmwareVersion struct {
		Current struct {
			VersionString string `json:"VersionString"`
		} `json:"Current"`
	} `json:"FirmwareVersion"`
	Status status `json:"Status"`
	Links  struct {
		PhysicalDrives odataRef `json:"PhysicalDrives"`
		LogicalDrives  odataRef `json:"LogicalDrives"`
	} `json:"Links"`
}
type hpePhysicalDrive struct {
	Model              string  `json:"Model"`
	SerialNumber       string  `json:"SerialNumber"`
	CapacityMiB        int64   `json:"CapacityMiB"`
	MediaType          string  `json:"MediaType"`     // HDD | SSD
	InterfaceType      string  `json:"InterfaceType"` // SAS | SATA | NVMe
	Location           string  `json:"Location"`
	RotationalSpeedRpm float64 `json:"RotationalSpeedRpm"` // iLO4 (0 for SSD)
	Status             status  `json:"Status"`
}
type hpeLogicalDrive struct {
	LogicalDriveName   string `json:"LogicalDriveName"`
	LogicalDriveNumber int    `json:"LogicalDriveNumber"` // iLO4 friendly index (Name is a raw GUID)
	Raid               string `json:"Raid"`               // HPE reports the RAID level as a bare number string ("1","5")
	CapacityMiB        int64  `json:"CapacityMiB"`
	Status             status `json:"Status"`
}

// collectHPESmartStorage walks the HPE OEM SmartStorage tree (array controllers →
// physical drives + logical drives) so HPE iLO servers expose disks/RAID even though they
// don't populate the standard Redfish Systems/Storage. Best-effort; any fetch error just
// leaves that branch empty.
func (f *BMCFacts) collectHPESmartStorage(ctx context.Context, c *Client, path string) {
	if path == "" {
		return
	}
	var ss hpeSmartStorage
	if err := c.GetJSON(ctx, path, &ss); err != nil {
		return
	}
	for _, acPath := range membersOf(ctx, c, ss.Links.ArrayControllers.id()) {
		var ac hpeArrayController
		if err := c.GetJSON(ctx, acPath, &ac); err != nil {
			continue
		}
		if ac.Model != "" {
			comp := Component{Kind: "controller", Name: ac.Model, Model: ac.Model, Serial: ac.SerialNumber, Status: ac.Status.Health, Detail: map[string]string{}}
			putStr(comp.Detail, "firmware", ac.FirmwareVersion.Current.VersionString)
			f.Components = append(f.Components, comp)
		}
		for _, ldPath := range membersOf(ctx, c, ac.Links.LogicalDrives.id()) {
			var ld hpeLogicalDrive
			if err := c.GetJSON(ctx, ldPath, &ld); err != nil {
				continue
			}
			// iLO4's LogicalDriveName is a raw hex GUID — prefer a friendly "Logical Drive N".
			vname := strings.TrimSpace(ld.LogicalDriveName)
			if vname == "" || len(vname) > 20 {
				if ld.LogicalDriveNumber > 0 {
					vname = fmt.Sprintf("Logical Drive %d", ld.LogicalDriveNumber)
				} else {
					vname = "Logical Drive"
				}
			}
			comp := Component{Kind: "volume", Name: vname, Status: ld.Status.Health,
				CapacityBytes: ld.CapacityMiB * 1024 * 1024, Detail: map[string]string{}}
			if ld.Raid != "" {
				comp.Detail["raid"] = "RAID" + ld.Raid
			}
			f.Components = append(f.Components, comp)
		}
		for _, pdPath := range membersOf(ctx, c, ac.Links.PhysicalDrives.id()) {
			var pd hpePhysicalDrive
			if err := c.GetJSON(ctx, pdPath, &pd); err != nil {
				continue
			}
			name := orDefault(pd.Location, orDefault(pd.SerialNumber, pd.Model))
			if name == "" {
				continue
			}
			comp := Component{Kind: "drive", Name: name, Model: strings.TrimSpace(pd.Model), Serial: strings.TrimSpace(pd.SerialNumber),
				Status: pd.Status.Health, CapacityBytes: pd.CapacityMiB * 1024 * 1024, Detail: map[string]string{}}
			putStr(comp.Detail, "media", pd.MediaType)
			putStr(comp.Detail, "protocol", pd.InterfaceType)
			putStr(comp.Detail, "location", pd.Location)
			if pd.RotationalSpeedRpm > 0 {
				putInt(comp.Detail, "rpm", int(pd.RotationalSpeedRpm))
			}
			f.Components = append(f.Components, comp)
		}
	}
}

func putStr(m map[string]string, k, v string) {
	if s := strings.TrimSpace(v); s != "" {
		m[k] = s
	}
}
func putInt(m map[string]string, k string, v int) {
	if v > 0 {
		m[k] = fmt.Sprintf("%d", v)
	}
}
func countComponentKind(cs []Component, kind string) int {
	n := 0
	for _, c := range cs {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

func membersOf(ctx context.Context, c *Client, collPath string) []string {
	var col collection
	if err := c.GetJSON(ctx, collPath, &col); err != nil {
		return nil
	}
	out := make([]string, 0, len(col.Members))
	for _, m := range col.Members {
		out = append(out, m.id())
	}
	return out
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
