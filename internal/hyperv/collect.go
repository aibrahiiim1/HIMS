// Package hyperv collects VM inventory from a Windows Hyper-V host over WinRM
// by running PowerShell (Get-VM) and parsing the JSON it emits. The transport
// (WinRM) can't be simulated like vcsim, so the design isolates the testable
// part: a Runner interface returns raw PowerShell stdout, and CollectVMs
// parses it — exercised with sample Get-VM payloads in collect_test.go. The
// real WinRM Runner lives in the collector's -hyperv mode and is marked
// live-validation-pending until run against a real Hyper-V host.
package hyperv

import (
	"context"
	"encoding/json"
	"strings"
)

// PowerShell that emits one compact JSON object/array the parser understands.
// MemoryStartup is bytes; we convert to MB. State is the VMState enum.
const GetVMScript = `Get-VM | Select-Object Name,State,ProcessorCount,` +
	`@{N='MemoryMB';E={[int]($_.MemoryStartup/1MB)}},` +
	`@{N='Guest';E={$_.Heartbeat}} | ConvertTo-Json -Compress`

// Runner executes a PowerShell script on the host and returns stdout. The real
// implementation wraps WinRM; tests inject a fake returning sample output.
type Runner interface {
	Run(ctx context.Context, script string) (string, error)
}

// VM is one Hyper-V virtual machine, normalized to our schema vocabulary.
type VM struct {
	Name       string
	PowerState string // on | off | suspended | unknown
	VCPU       int32
	MemoryMB   int32
	GuestOS    string
}

// rawVM mirrors the Get-VM JSON; State is flexible (enum int or string).
type rawVM struct {
	Name           string          `json:"Name"`
	State          json.RawMessage `json:"State"`
	ProcessorCount int32           `json:"ProcessorCount"`
	MemoryMB       int32           `json:"MemoryMB"`
	Guest          string          `json:"Guest"`
}

// CollectVMs runs Get-VM and parses the result. ConvertTo-Json emits a bare
// object for a single VM and an array for many — both are handled.
func CollectVMs(ctx context.Context, r Runner) ([]VM, error) {
	out, err := r.Run(ctx, GetVMScript)
	if err != nil {
		return nil, err
	}
	return parseVMs(out)
}

func parseVMs(out string) ([]VM, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var raws []rawVM
	if out[0] == '[' {
		if err := json.Unmarshal([]byte(out), &raws); err != nil {
			return nil, err
		}
	} else {
		var one rawVM
		if err := json.Unmarshal([]byte(out), &one); err != nil {
			return nil, err
		}
		raws = []rawVM{one}
	}
	vms := make([]VM, 0, len(raws))
	for _, rv := range raws {
		vms = append(vms, VM{
			Name: rv.Name, PowerState: mapState(rv.State),
			VCPU: rv.ProcessorCount, MemoryMB: rv.MemoryMB, GuestOS: rv.Guest,
		})
	}
	return vms, nil
}

// mapState normalizes the Hyper-V VMState enum (rendered by ConvertTo-Json as
// an integer, or sometimes a string) to our vocabulary.
//
//	2 = Running, 3 = Off, 6/9 = Saved/Paused (→ suspended).
func mapState(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	switch strings.ToLower(s) {
	case "2", "running":
		return "on"
	case "3", "off":
		return "off"
	case "6", "9", "saved", "paused", "suspended":
		return "suspended"
	default:
		return "unknown"
	}
}
