package githubrel

import (
	"fmt"
	"math"
)

// FormatRate renders an average transfer speed.
func FormatRate(bytesPerSec float64) string {
	if math.IsNaN(bytesPerSec) || bytesPerSec < 0 {
		bytesPerSec = 0
	}
	const (
		kb = 1000.0
		mb = 1000.0 * kb
		gb = 1000.0 * mb
	)
	switch {
	case bytesPerSec >= gb:
		return fmt.Sprintf("%.2f GB/s", bytesPerSec/gb)
	case bytesPerSec >= mb:
		return fmt.Sprintf("%.1f MB/s", bytesPerSec/mb)
	case bytesPerSec >= kb:
		return fmt.Sprintf("%.1f KB/s", bytesPerSec/kb)
	default:
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
}

// FormatProgress is a single status line: name, percentage, then the data rate.
func FormatProgress(p Progress) string {
	pct := "--%"
	if p.Total > 0 {
		percent := 100 * float64(p.Received) / float64(p.Total)
		if percent < 0 {
			percent = 0
		}
		if percent > 100 || p.Done {
			percent = 100
		}
		if p.Done || percent >= 100 {
			pct = "100%"
		} else {
			pct = fmt.Sprintf("%.1f%%", percent)
		}
	} else if p.Done {
		pct = "100%"
	}
	return fmt.Sprintf("%s  %s  %s", p.Name, pct, FormatRate(p.BytesPerSec))
}
