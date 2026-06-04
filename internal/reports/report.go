// Package reports builds exportable report documents (CSV / XLSX) from
// in-memory tabular data. It is the rendering core behind Reports Pro (#21):
// the API assembles a Report from DB queries, and these encoders turn it into a
// downloadable file or an email-able summary. Pure data-in/bytes-out — no DB,
// no HTTP — so the encoders are unit-tested.
package reports

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"time"

	"github.com/xuri/excelize/v2"
)

// Sheet is one tab/table of a report.
type Sheet struct {
	Name    string
	Headers []string
	Rows    [][]string
}

// Report is a titled, multi-sheet document.
type Report struct {
	Title     string
	Generated time.Time
	Sheets    []Sheet
}

// Summary renders a short plain-text digest (title + per-sheet row counts),
// suitable for a scheduled-email body.
func (r Report) Summary() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\nGenerated: %s\n\n", r.Title, r.Generated.Format("2006-01-02 15:04 MST"))
	for _, s := range r.Sheets {
		fmt.Fprintf(&b, "  • %s: %d row(s)\n", s.Name, len(s.Rows))
	}
	return b.String()
}

// CSV renders the report as CSV. With a single sheet it is a plain table; with
// multiple sheets each is prefixed by a "# <sheet name>" banner row so one file
// still carries everything.
func (r Report) CSV() ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	for i, s := range r.Sheets {
		if len(r.Sheets) > 1 {
			if i > 0 {
				_ = w.Write([]string{})
			}
			_ = w.Write([]string{"# " + s.Name})
		}
		if len(s.Headers) > 0 {
			_ = w.Write(s.Headers)
		}
		for _, row := range s.Rows {
			_ = w.Write(row)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// XLSX renders the report as a real .xlsx workbook — one worksheet per sheet,
// a bold header row, and an auto-filter over the data range.
func (r Report) XLSX() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return nil, err
	}

	first := ""
	for i, s := range r.Sheets {
		name := sanitizeSheetName(s.Name, i)
		if i == 0 {
			// excelize starts with a default "Sheet1"; rename it.
			_ = f.SetSheetName("Sheet1", name)
			first = name
		} else if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
		// Header row.
		for c, h := range s.Headers {
			cell, _ := excelize.CoordinatesToCellName(c+1, 1)
			_ = f.SetCellValue(name, cell, h)
		}
		if len(s.Headers) > 0 {
			lastCol, _ := excelize.CoordinatesToCellName(len(s.Headers), 1)
			_ = f.SetCellStyle(name, "A1", lastCol, bold)
			endCol, _ := excelize.CoordinatesToCellName(len(s.Headers), len(s.Rows)+1)
			_ = f.AutoFilter(name, "A1:"+endCol, []excelize.AutoFilterOptions{})
		}
		// Data rows.
		for ri, row := range s.Rows {
			for c, v := range row {
				cell, _ := excelize.CoordinatesToCellName(c+1, ri+2)
				_ = f.SetCellValue(name, cell, v)
			}
		}
	}
	if first != "" {
		if idx, err := f.GetSheetIndex(first); err == nil {
			f.SetActiveSheet(idx)
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sanitizeSheetName keeps within Excel's 31-char limit and avoids the illegal
// characters []:*?/\ ; falls back to a positional name if empty.
func sanitizeSheetName(name string, idx int) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch r {
		case '[', ']', ':', '*', '?', '/', '\\':
			out = append(out, ' ')
		default:
			out = append(out, r)
		}
	}
	s := string(out)
	if s == "" {
		s = fmt.Sprintf("Sheet%d", idx+1)
	}
	if len(s) > 31 {
		s = s[:31]
	}
	return s
}
