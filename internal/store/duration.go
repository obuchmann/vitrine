package store

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDuration accepts Go durations ("36h", "90m") and whole days ("10d").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q: use e.g. 10d or 36h", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q: use e.g. 10d or 36h", s)
	}
	return d, nil
}

// FormatDuration writes whole days as "10d" and everything else as a Go duration.
func FormatDuration(d time.Duration) string {
	if d > 0 && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}
