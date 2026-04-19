package token

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseExpires accepts any of the following and returns the absolute expiry
// time (nil = never):
//
//	""         → never
//	"never"    → never
//	"30d"      → 30 days from now
//	"7d" "1w"  → weeks (w) and days (d) supported in addition to Go's h/m/s
//	"2h30m"    → any Go duration
//	"2025-12-31"              → end of that date (UTC midnight + 24h)
//	"2025-12-31T12:00:00Z"    → any RFC3339 timestamp
func ParseExpires(s string, now time.Time) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "never") {
		return nil, nil
	}

	if d, ok := parseExtendedDuration(s); ok {
		if d <= 0 {
			return nil, fmt.Errorf("expires duration must be positive (got %q)", s)
		}
		t := now.Add(d).UTC()
		return &t, nil
	}

	if t, err := time.Parse(time.RFC3339, s); err == nil {
		t = t.UTC()
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		// Date-only → end of that day UTC (start of next day).
		t = t.Add(24 * time.Hour).UTC()
		return &t, nil
	}

	return nil, fmt.Errorf("unparseable expires value %q (try 30d, 2025-12-31, or never)", s)
}

func parseExtendedDuration(s string) (time.Duration, bool) {
	if len(s) < 2 {
		return 0, false
	}
	last := s[len(s)-1]
	if last == 'd' || last == 'w' {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, false
		}
		mult := 24 * time.Hour
		if last == 'w' {
			mult = 7 * 24 * time.Hour
		}
		return time.Duration(n) * mult, true
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, true
	}
	return 0, false
}
