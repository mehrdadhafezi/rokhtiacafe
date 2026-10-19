package textutil

import "testing"

func TestLine(t *testing.T) {
	cases := map[string]string{
		"  اسپرسو  ":              "اسپرسو",
		"Latte\x00\x07 Macchiato": "Latte Macchiato",
		"a\n\tb   c":              "a b c",
		"café":                   "café", // NFC composes e + combining acute
		"‏رخ":                     "‏رخ",  // RTL mark is a format char, not a control: kept
	}
	for in, want := range cases {
		if got := Line(in); got != want {
			t.Errorf("Line(%q) = %q, want %q", in, got, want)
		}
	}
	if Len("کیک") != 3 {
		t.Error("Len must count characters")
	}
}

func TestMultiline(t *testing.T) {
	if got := Multiline("  first line \r\n\x00second\x1b line  \n"); got != "first line\nsecond line" {
		t.Errorf("Multiline = %q", got)
	}
}
