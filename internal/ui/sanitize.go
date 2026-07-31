package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

func EscapeBytes(data []byte) string {
	var b strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&b, `\x%02X`, data[0])
			data = data[1:]
			continue
		}
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			for _, value := range data[:size] {
				fmt.Fprintf(&b, `\x%02X`, value)
			}
		default:
			b.WriteRune(r)
		}
		data = data[size:]
	}
	return b.String()
}

func EscapeText(value string) string { return EscapeBytes([]byte(value)) }

func Truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	leftWidth := (width - 3 + 1) / 2
	rightWidth := width - 3 - leftWidth
	return takeCells(value, leftWidth, false) + "..." + takeCells(value, rightWidth, true)
}

func takeCells(value string, width int, fromEnd bool) string {
	runes := []rune(value)
	if fromEnd {
		for start := 0; start <= len(runes); start++ {
			candidate := string(runes[start:])
			if lipgloss.Width(candidate) > width {
				continue
			}
			return candidate
		}
		return ""
	}
	for end := len(runes); end >= 0; end-- {
		candidate := string(runes[:end])
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return ""
}
