// Package textutil normalizes user-entered text (docs/SECURITY.md §6): Unicode NFC, control
// characters removed, surrounding space trimmed. Lengths are counted in characters.
package textutil

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Line cleans single-line text (names, labels): every control character is removed and
// internal whitespace runs collapse to one space.
func Line(s string) string {
	s = norm.NFC.String(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// Multiline cleans free text (descriptions, notes): keeps line breaks, removes other controls.
func Multiline(s string) string {
	s = norm.NFC.String(strings.ReplaceAll(s, "\r\n", "\n"))
	s = strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Len counts characters, not bytes.
func Len(s string) int { return utf8.RuneCountInString(s) }

// PersianDigits replaces ASCII digits with Persian ones (user-facing text such as notifications).
func PersianDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '۰' + (r - '0')
		}
		return r
	}, s)
}
