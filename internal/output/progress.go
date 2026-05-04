package output

import "strings"

// FormatProgress renders a text progress bar.
// pct: 0.0–1.0  width: total character width including brackets
// If width <= 0, defaults to 40. Returns "" when width < 2 (after defaulting).
func FormatProgress(pct float64, width int) string {
	if width <= 0 {
		width = 40
	}
	if width < 2 {
		return ""
	}
	inner := width - 2
	filled := int(pct * float64(inner))
	if filled > inner {
		filled = inner
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", inner-filled) + "]"
}
