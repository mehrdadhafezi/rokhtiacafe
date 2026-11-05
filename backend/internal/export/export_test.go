package export_test

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/export"
)

func TestRisky(t *testing.T) {
	for _, s := range []string{"=1+1", "+1", "-2", "@SUM(A1)", "\tx", "\rx", "\nx", "  =cmd", "＝1", "＋1"} {
		if !export.Risky(s) {
			t.Errorf("%q should be risky", s)
		}
	}
	for _, s := range []string{"", "مریم", "Ali", "0912", "a=b", "1-2", " "} {
		if export.Risky(s) {
			t.Errorf("%q should not be risky", s)
		}
	}
}

func TestCSV(t *testing.T) {
	var buf bytes.Buffer
	err := export.WriteCSV(&buf, export.Sheet{Header: []string{"نام", "مبلغ"}, Rows: [][]export.Cell{
		{export.T("=cmd|' /C calc'!A0"), export.N(-1500)},
		{export.T("سارا, \"خانم\"\nخط دوم"), export.N(0)},
		{export.T("a\x00b\x07c"), export.N(9223372036854775807)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if !bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		t.Fatal("no BOM")
	}
	recs, err := csv.NewReader(bytes.NewReader(raw[3:])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"نام", "مبلغ"}, {"'=cmd|' /C calc'!A0", "-1500"}, {"سارا, \"خانم\"\nخط دوم", "0"}, {"abc", "9223372036854775807"}}
	for i := range want {
		if strings.Join(recs[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("row %d = %q, want %q", i, recs[i], want[i])
		}
	}
}

func TestXLSXIsWellFormedAndFormulaFree(t *testing.T) {
	var buf bytes.Buffer
	sheets := []export.Sheet{
		{Name: "مشتریان", Header: []string{"نام", "مبلغ"}, Rows: [][]export.Cell{
			{export.T("=1+1"), export.N(1900000)}, {export.T("<b>&\"x\"</b>\x01"), export.N(-5)}}},
		{Name: "a/b:c*?[d]", Header: []string{"x"}},
		{Name: "مشتریان", Header: []string{"y"}}, // duplicate name → renamed
	}
	if err := export.WriteXLSX(&buf, true, sheets...); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if z.File[0].Name != "[Content_Types].xml" {
		t.Errorf("first part = %s", z.File[0].Name)
	}
	parts := map[string]string{}
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		parts[f.Name] = string(b)
		// Every part must be well-formed XML.
		d := xml.NewDecoder(bytes.NewReader(b))
		for {
			if _, err := d.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed: %v", f.Name, err)
			}
		}
	}
	s1 := parts["xl/worksheets/sheet1.xml"]
	if strings.Contains(s1, "<f") {
		t.Error("formula element present")
	}
	if !strings.Contains(s1, `<c r="A2" t="inlineStr" s="3"><is><t xml:space="preserve">=1+1</t></is></c>`) {
		t.Errorf("risky cell = %s", s1)
	}
	if !strings.Contains(s1, `<c r="B2" s="2"><v>1900000</v></c>`) || !strings.Contains(s1, `&lt;b&gt;&amp;&quot;x&quot;&lt;/b&gt;</t>`) {
		t.Errorf("cells = %s", s1)
	}
	wb := parts["xl/workbook.xml"]
	for _, n := range []string{`name="مشتریان"`, `name="abcd"`, `name="Sheet3"`} {
		if !strings.Contains(wb, n) {
			t.Errorf("workbook lacks %s: %s", n, wb)
		}
	}
}

func TestColumnName(t *testing.T) {
	for i, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"} {
		if got := export.ColumnName(i); got != want {
			t.Errorf("ColumnName(%d) = %s, want %s", i, got, want)
		}
	}
}
