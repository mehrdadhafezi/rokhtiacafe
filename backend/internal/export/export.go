// Package export writes report tables as CSV (UTF-8 with BOM, for Excel and Persian text) and as
// real Office Open XML workbooks (.xlsx), with protection against spreadsheet formula injection.
//
// Injection rules (OWASP "CSV injection"): a text cell whose first non-blank character is one of
// = + - @ (or their full-width forms), or that starts with a tab, CR or LF, is neutralised.
//   - CSV: the text is prefixed with an apostrophe, so no spreadsheet evaluates it.
//   - XLSX: every text cell is an inline string (never a formula), and risky text additionally gets
//     Excel's quotePrefix style, so it stays literal even when a user edits the cell.
//
// Numbers are written as numbers. Characters that XML 1.0 forbids are removed from all text.
package export

import (
	"archive/zip"
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Cell is one value: text, or a whole number (rials, counts).
type Cell struct {
	Text  string
	Num   int64
	IsNum bool
}

// T is a text cell.
func T(s string) Cell { return Cell{Text: s} }

// N is a number cell.
func N(n int64) Cell { return Cell{Num: n, IsNum: true} }

// Sheet is one table with a header row.
type Sheet struct {
	Name   string
	Header []string
	Rows   [][]Cell
}

// Risky reports whether a spreadsheet could interpret s as a formula or command.
func Risky(s string) bool {
	if s == "" {
		return false
	}
	switch s[0] {
	case '\t', '\r', '\n':
		return true
	}
	t := strings.TrimLeftFunc(s, unicode.IsSpace)
	if t == "" {
		return false
	}
	switch []rune(t)[0] {
	case '=', '+', '-', '@', '＝', '＋', '－', '＠':
		return true
	}
	return false
}

// clean removes characters that are invalid in XML 1.0 (and in practice break spreadsheets).
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20, r >= 0x7F && r <= 0x9F, r == 0xFFFE, r == 0xFFFF:
			return -1
		}
		return r
	}, s)
}

// NeutralizeCSV returns s safe to place in a CSV cell.
func NeutralizeCSV(s string) string {
	s = clean(s)
	if Risky(s) {
		return "'" + s
	}
	return s
}

// WriteCSV writes the sheet as UTF-8 CSV with a byte-order mark (Excel then reads Persian text).
func WriteCSV(w io.Writer, s Sheet) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(bw)
	cw.UseCRLF = true
	header := make([]string, len(s.Header))
	for i, h := range s.Header {
		header[i] = NeutralizeCSV(h)
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	rec := make([]string, 0, len(s.Header))
	for _, row := range s.Rows {
		rec = rec[:0]
		for _, c := range row {
			if c.IsNum {
				rec = append(rec, strconv.FormatInt(c.Num, 10))
			} else {
				rec = append(rec, NeutralizeCSV(c.Text))
			}
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return bw.Flush()
}

// ---- XLSX ----

const (
	nsMain = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	nsRel  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsPkg  = "http://schemas.openxmlformats.org/package/2006/relationships"
	xmlHdr = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
)

// Style indexes in styles.xml.
const (
	styleText   = 0
	styleHeader = 1
	styleNumber = 2
	styleQuoted = 3 // quotePrefix: literal text even if it looks like a formula
)

const stylesXML = xmlHdr + `<styleSheet xmlns="` + nsMain + `">` +
	`<fonts count="2"><font><sz val="11"/><name val="Tahoma"/></font><font><b/><sz val="11"/><name val="Tahoma"/></font></fonts>` +
	`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
	`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="4">` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
	`<xf numFmtId="3" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`<xf numFmtId="49" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" quotePrefix="1"/>` +
	`</cellXfs>` +
	`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
	`</styleSheet>`

// ColumnName returns the spreadsheet column letters for a zero-based index (0 → A, 26 → AA).
func ColumnName(i int) string {
	var b []byte
	for i++; i > 0; i = (i - 1) / 26 {
		b = append([]byte{byte('A' + (i-1)%26)}, b...)
	}
	return string(b)
}

func escapeXML(s string) string {
	var b strings.Builder
	for _, r := range clean(s) {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\r':
			b.WriteString("&#13;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sheetName returns a valid, unique worksheet name (≤ 31 characters, none of []:*?/\).
func sheetName(name string, i int, used map[string]bool) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) || r < 0x20 {
			return -1
		}
		return r
	}, name)
	if r := []rune(name); len(r) > 31 {
		name = string(r[:31])
	}
	if strings.TrimSpace(name) == "" || used[strings.ToLower(name)] {
		name = fmt.Sprintf("Sheet%d", i+1)
	}
	used[strings.ToLower(name)] = true
	return name
}

func writeTextCell(b *strings.Builder, ref string, text string, style int) {
	if Risky(text) && style == styleText {
		style = styleQuoted
	}
	fmt.Fprintf(b, `<c r="%s" t="inlineStr"`, ref)
	if style != styleText {
		fmt.Fprintf(b, ` s="%d"`, style)
	}
	b.WriteString(`><is><t xml:space="preserve">`)
	b.WriteString(escapeXML(text))
	b.WriteString(`</t></is></c>`)
}

func sheetXML(s Sheet, rtl bool) string {
	var b strings.Builder
	b.WriteString(xmlHdr)
	b.WriteString(`<worksheet xmlns="` + nsMain + `"><sheetViews><sheetView workbookViewId="0"`)
	if rtl {
		b.WriteString(` rightToLeft="1"`)
	}
	b.WriteString(`><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	if len(s.Header) > 0 {
		fmt.Fprintf(&b, `<cols><col min="1" max="%d" width="18" customWidth="1"/></cols>`, len(s.Header))
	}
	b.WriteString(`<sheetData>`)
	b.WriteString(`<row r="1">`)
	for i, h := range s.Header {
		writeTextCell(&b, ColumnName(i)+"1", h, styleHeader)
	}
	b.WriteString(`</row>`)
	for ri, row := range s.Rows {
		r := strconv.Itoa(ri + 2)
		fmt.Fprintf(&b, `<row r="%s">`, r)
		for ci, c := range row {
			ref := ColumnName(ci) + r
			if c.IsNum {
				fmt.Fprintf(&b, `<c r="%s" s="%d"><v>%d</v></c>`, ref, styleNumber, c.Num)
			} else {
				writeTextCell(&b, ref, c.Text, styleText)
			}
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

// WriteXLSX writes the sheets as one .xlsx workbook. rtl sets right-to-left sheets (Persian).
func WriteXLSX(w io.Writer, rtl bool, sheets ...Sheet) error {
	if len(sheets) == 0 {
		return fmt.Errorf("export: no sheets")
	}
	z := zip.NewWriter(w)
	put := func(name, content string) error {
		f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, content)
		return err
	}
	var ct, wb, rels strings.Builder
	ct.WriteString(xmlHdr + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	wb.WriteString(xmlHdr + `<workbook xmlns="` + nsMain + `" xmlns:r="` + nsRel + `"><sheets>`)
	rels.WriteString(xmlHdr + `<Relationships xmlns="` + nsPkg + `">`)
	used := map[string]bool{}
	for i, s := range sheets {
		n := i + 1
		fmt.Fprintf(&ct, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, n)
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, escapeXML(sheetName(s.Name, i, used)), n, n)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="%s/worksheet" Target="worksheets/sheet%d.xml"/>`, n, nsRel, n)
	}
	ct.WriteString(`</Types>`)
	wb.WriteString(`</sheets></workbook>`)
	fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="%s/styles" Target="styles.xml"/></Relationships>`, len(sheets)+1, nsRel)

	files := []struct{ name, content string }{
		{"[Content_Types].xml", ct.String()},
		{"_rels/.rels", xmlHdr + `<Relationships xmlns="` + nsPkg + `"><Relationship Id="rId1" Type="` + nsRel + `/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", wb.String()},
		{"xl/_rels/workbook.xml.rels", rels.String()},
		{"xl/styles.xml", stylesXML},
	}
	for _, f := range files {
		if err := put(f.name, f.content); err != nil {
			return err
		}
	}
	for i, s := range sheets {
		if err := put(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheetXML(s, rtl)); err != nil {
			return err
		}
	}
	return z.Close()
}
