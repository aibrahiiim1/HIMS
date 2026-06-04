// Package config implements the pure logic behind Config Backup (#10) and
// Config Drift (#11): the per-vendor command that dumps a running-config, a
// stable content hash for change detection, and a unified line diff between two
// captured versions. It has no I/O — the SSH transport and storage live
// elsewhere — so every function here is deterministic and unit-tested.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// CommandFor returns the CLI command that prints the full running configuration
// for a device driver, or "" if the driver is not a known CLI config target.
// The caller treats "" as "this device type can't be config-backed-up over SSH".
func CommandFor(driver string) string {
	switch driver {
	case "cisco_ios", "aruba_hpe", "extreme_switch", "arista_eos":
		return "show running-config"
	case "fortigate":
		return "show full-configuration"
	case "huawei_vrp":
		return "display current-configuration"
	case "juniper_junos":
		return "show configuration | display set"
	case "paloalto_panos":
		return "show config running"
	default:
		return ""
	}
}

// Hash returns the SHA-256 (hex) of the config content after normalisation.
// Drift detection compares hashes: equal hash == no change.
func Hash(content string) string {
	sum := sha256.Sum256([]byte(Normalize(content)))
	return hex.EncodeToString(sum[:])
}

// Normalize strips trailing whitespace per line and trailing blank lines, and
// converts CRLF to LF, so cosmetic terminal differences (a switch padding lines
// or sending CRLF) don't register as config drift. Leading/internal content is
// preserved exactly.
func Normalize(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	// Drop trailing blank lines.
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return strings.Join(lines[:end], "\n")
}

// DiffStat summarises a diff: number of added and removed lines.
type DiffStat struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// DiffLine is one line of a unified diff. Op is ' ' (context), '+' (added in
// b), or '-' (removed from a).
type DiffLine struct {
	Op   byte   `json:"op"`
	Text string `json:"text"`
}

// Diff computes a line-level diff between a (old) and b (new) using a longest-
// common-subsequence backtrack. It returns the full line sequence (context +
// changes) and the add/remove counts. Inputs are normalised first so cosmetic
// noise is ignored — the same normalisation Hash uses, keeping "changed" and
// "the diff is empty" consistent.
func Diff(a, b string) ([]DiffLine, DiffStat) {
	al := splitLines(Normalize(a))
	bl := splitLines(Normalize(b))

	// LCS length table.
	n, m := len(al), len(bl)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var out []DiffLine
	var stat DiffStat
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case al[i] == bl[j]:
			out = append(out, DiffLine{Op: ' ', Text: al[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Op: '-', Text: al[i]})
			stat.Removed++
			i++
		default:
			out = append(out, DiffLine{Op: '+', Text: bl[j]})
			stat.Added++
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Op: '-', Text: al[i]})
		stat.Removed++
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{Op: '+', Text: bl[j]})
		stat.Added++
	}
	return out, stat
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
