// Package omnipcx is a minimal, READ-ONLY connector for the Alcatel-Lucent OmniPCX Enterprise
// call server, which exposes management over TELNET only (no SSH). It logs in with a CLI
// credential (e.g. mtcl/mtcl) and reads the login banner ("Application software identity"),
// which carries the real software version/release/patch/country/CPU-role — enough to identify
// + manage the PBX honestly. It issues NO configuration commands.
//
// SCOPE: identity only. The user/extension DIRECTORY and registered phone SETS are NOT
// available over this path — VERIFIED live against a real OXE: the mtcl telnet account exposes
// no usable command shell (the session echoes input but there is no prompt and no command —
// mgr/ls/cat/echo/whoami — executes or returns output), and SNMP exposes only identity (2
// status OIDs under .1.3.6.1.4.1.637, no telephony tables). The directory/sets live in the
// call-server DB reachable only via OmniVista 8770 / the OXE management (CSTA) API — an honest
// external dependency, never fabricated here.
package omnipcx

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/telnet"
)

// Identity is what a safe OmniPCX login can read from the banner. Empty fields = not exposed
// (never fabricated).
type Identity struct {
	Connected bool
	Vendor    string // "Alcatel-Lucent"
	Model     string // "OmniPCX Enterprise"
	Software  string // full identity, e.g. "R7.1-f5.401-29-a-eg-c6s2"
	Release   string // "R7.1"
	Delivery  string // "f5.401"
	Patch     string // "29"
	Country   string // "eg"
	CPURole   string // "MAIN" | "STANDBY"
	Reason    string // honest failure reason when not connected
}

var (
	reSoftware = regexp.MustCompile(`(?m)^\s*([A-Z]\d+(?:\.\d+)?-[a-z]\d+\.\d+-\d+-[a-z0-9]+-[a-z0-9]+-[a-z0-9]+)\s*$`)
	reBusiness = regexp.MustCompile(`(?i)Business identification:\s*([^\r\n]+)`)
	reDelivery = regexp.MustCompile(`(?i)DELIVERY\s+([^\r\n]+)`)
	rePatch    = regexp.MustCompile(`(?i)Patch identification:\s*([^\r\n]+)`)
	reCountry  = regexp.MustCompile(`(?i)Country:\s*([^\r\n]+)`)
	reRole     = regexp.MustCompile(`(?i)role of the CPU is\s+([A-Za-z]+)`)
)

// Probe logs in over telnet and reads the OmniPCX login banner for identity. Read-only: it
// sends only the credentials, then `exit`. Returns Connected=false with an honest Reason on
// unreachable / auth failure / not-an-OmniPCX.
func Probe(ctx context.Context, host string, port int, user, pass string) Identity {
	if port == 0 {
		port = 23
	}
	c, err := telnet.Dial(ctx, fmt.Sprintf("%s:%d", host, port), 8*time.Second)
	if err != nil {
		return Identity{Reason: "unreachable: " + shortErr(err)}
	}
	defer c.Close()

	if _, e := c.ReadUntil(12*time.Second, "login:", "ogin:", "username:"); e != nil {
		return Identity{Reason: "protocol: no telnet login prompt"}
	}
	_ = c.Write(user + "\r\n")
	if _, e := c.ReadUntil(10*time.Second, "password:", "assword"); e != nil {
		return Identity{Reason: "protocol: no password prompt"}
	}
	_ = c.Write(pass + "\r\n")
	banner, _ := c.ReadUntilIdle(3*time.Second, 20*time.Second)
	_ = c.Write("exit\r\n")

	low := strings.ToLower(banner)
	// A rejected login re-prompts "login:" (telnet) or says "incorrect".
	if strings.Contains(low, "incorrect") || strings.Contains(low, "login incorrect") ||
		(strings.Contains(low, "login:") && !strings.Contains(low, "omnipcx")) {
		return Identity{Reason: "credential_failed: telnet login rejected"}
	}
	if !strings.Contains(low, "omnipcx") {
		return Identity{Reason: "protocol_not_supported: not an OmniPCX login banner"}
	}

	id := Identity{Connected: true, Vendor: "Alcatel-Lucent", Model: "OmniPCX Enterprise"}
	if m := reSoftware.FindStringSubmatch(banner); m != nil {
		id.Software = strings.TrimSpace(m[1])
	}
	if m := reBusiness.FindStringSubmatch(banner); m != nil {
		id.Release = strings.TrimSpace(m[1])
	}
	if m := reDelivery.FindStringSubmatch(banner); m != nil {
		id.Delivery = strings.TrimSpace(m[1])
	}
	if m := rePatch.FindStringSubmatch(banner); m != nil {
		id.Patch = strings.TrimSpace(m[1])
	}
	if m := reCountry.FindStringSubmatch(banner); m != nil {
		id.Country = strings.TrimSpace(m[1])
	}
	if m := reRole.FindStringSubmatch(banner); m != nil {
		id.CPURole = strings.ToUpper(strings.TrimSpace(m[1]))
	}
	return id
}

// OSVersion is the normalized software string for the device row (release + delivery + patch).
func (id Identity) OSVersion() string {
	if id.Software != "" {
		return id.Software
	}
	parts := []string{}
	if id.Release != "" {
		parts = append(parts, id.Release)
	}
	if id.Delivery != "" {
		parts = append(parts, id.Delivery)
	}
	if id.Patch != "" {
		parts = append(parts, "patch "+id.Patch)
	}
	return strings.Join(parts, " ")
}

func shortErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
