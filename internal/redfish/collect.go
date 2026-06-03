package redfish

import (
	"context"
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
	MemoryGiB       float64
	Sensors         []Sensor
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
	ID string `json:"@odata.id"`
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
	Storage odataRef `json:"Storage"`
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
	Model           string `json:"Model"`
	FirmwareVersion string `json:"FirmwareVersion"`
}
type storageMember struct {
	Name   string     `json:"Name"`
	Status status     `json:"Status"`
	Drives []odataRef `json:"Drives"`
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
	if sysPath := firstMember(ctx, c, root.Systems.ID); sysPath != "" {
		var sys computerSystem
		if err := c.GetJSON(ctx, sysPath, &sys); err == nil {
			if f.Vendor == "" {
				f.Vendor = sys.Manufacturer
			}
			f.Model, f.SKU, f.Serial = sys.Model, sys.SKU, sys.SerialNumber
			f.BiosVersion, f.PowerState, f.Health = sys.BiosVersion, sys.PowerState, sys.Status.Health
			f.ProcessorCount, f.ProcessorModel = sys.ProcessorSum.Count, sys.ProcessorSum.Model
			f.MemoryGiB = sys.MemorySum.TotalSystemMemoryGiB
			f.collectStorage(ctx, c, sys.Storage.ID)
		}
	}

	// Chassis thermal + power (first member).
	if chPath := firstMember(ctx, c, root.Chassis.ID); chPath != "" {
		var ch chassis
		if err := c.GetJSON(ctx, chPath, &ch); err == nil {
			f.collectThermal(ctx, c, ch.Thermal.ID)
			f.collectPower(ctx, c, ch.Power.ID)
		}
	}

	// Manager firmware (iLO/iDRAC version).
	if mgrPath := firstMember(ctx, c, root.Managers.ID); mgrPath != "" {
		var mgr manager
		if err := c.GetJSON(ctx, mgrPath, &mgr); err == nil {
			f.FirmwareVersion = mgr.FirmwareVersion
			if f.ControllerKind == "redfish" && mgr.Model != "" {
				f.ControllerKind = mgr.Model
			}
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
	return col.Members[0].ID
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

func (f *BMCFacts) collectStorage(ctx context.Context, c *Client, path string) {
	if path == "" {
		return
	}
	for _, m := range membersOf(ctx, c, path) {
		var sm storageMember
		if err := c.GetJSON(ctx, m, &sm); err != nil {
			continue
		}
		f.Sensors = append(f.Sensors, Sensor{
			Kind: "storage", Name: orDefault(sm.Name, "Storage"), Status: sm.Status.Health,
			Reading: float64(len(sm.Drives)), Unit: "drives", HasReading: len(sm.Drives) > 0,
		})
	}
}

func membersOf(ctx context.Context, c *Client, collPath string) []string {
	var col collection
	if err := c.GetJSON(ctx, collPath, &col); err != nil {
		return nil
	}
	out := make([]string, 0, len(col.Members))
	for _, m := range col.Members {
		out = append(out, m.ID)
	}
	return out
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
