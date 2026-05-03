package output

import (
	"fmt"
	"strings"
)

// FormatProgress returns a human-readable progress bar string.
// pct should be 0.0–1.0.
func FormatProgress(pct float64, width int) string {
	if width <= 0 {
		width = 40
	}
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled
	return fmt.Sprintf("[%s%s] %.1f%%",
		strings.Repeat("=", filled),
		strings.Repeat(" ", empty),
		pct*100,
	)
}
