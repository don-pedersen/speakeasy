package token

import (
	"strings"
	"testing"
	"time"
)

func TestParseExpires(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		in   string
		want *time.Time
		err  string
	}{
		{in: "", want: nil},
		{in: "never", want: nil},
		{in: "NEVER", want: nil},
		{in: "30d", want: tp(now.Add(30 * 24 * time.Hour))},
		{in: "1w", want: tp(now.Add(7 * 24 * time.Hour))},
		{in: "2h", want: tp(now.Add(2 * time.Hour))},
		{in: "2h30m", want: tp(now.Add(2*time.Hour + 30*time.Minute))},
		{in: "2025-12-31", want: tp(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))},
		{in: "2025-12-31T06:00:00Z", want: tp(time.Date(2025, 12, 31, 6, 0, 0, 0, time.UTC))},
		{in: "0d", err: "positive"},
		{in: "-5d", err: "positive"},
		{in: "xyz", err: "unparseable"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseExpires(tc.in, now)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("want error containing %q, got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch {
			case got == nil && tc.want == nil:
			case got == nil || tc.want == nil:
				t.Fatalf("got=%v want=%v", got, tc.want)
			case !got.Equal(*tc.want):
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func tp(t time.Time) *time.Time { return &t }
