// Package cron parses a tiny subset of cron expressions plus a few
// convenience aliases (@hourly, @daily, @weekly, @every 5m). It's
// deliberately stdlib-only so salience keeps its single-binary story.
//
// Supported expression formats:
//   - @hourly                 → minute 0 of every hour
//   - @daily                  → 00:00 every day
//   - @weekly                 → 00:00 every Monday
//   - @every <Go duration>    → fire on a fixed cadence (e.g. @every 30m)
//   - "M H D Mo W"            → standard 5-field cron, each field accepts:
//        *           — any
//        N           — exact value
//        N,N,N       — list
//        N-N         — range
//        */N         — step ("every N")
//   - L (the day-of-week field) accepts 0-6 with 0 = Sunday, OR
//     mon|tue|wed|thu|fri|sat|sun (case-insensitive).
//
// This is plenty for the "run me weekly on Monday morning" use case
// salience needs. For complex enterprise cron you'd reach for robfig/cron.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is the parsed shape of a cron expression.
type Schedule struct {
	Minutes  []int
	Hours    []int
	Days     []int   // day of month, 1-31
	Months   []int   // 1-12
	Weekdays []int   // 0-6, Sunday=0
	Every    time.Duration // non-zero ⇒ this overrides the field-based schedule
}

// Parse turns an expression string into a Schedule. Returns an error for
// invalid input.
func Parse(expr string) (*Schedule, error) {
	expr = strings.TrimSpace(expr)
	switch strings.ToLower(expr) {
	case "@hourly":
		return parseFields("0 * * * *")
	case "@daily":
		return parseFields("0 0 * * *")
	case "@weekly":
		return parseFields("0 0 * * 1")
	}
	if strings.HasPrefix(expr, "@every ") {
		d, err := time.ParseDuration(strings.TrimPrefix(expr, "@every "))
		if err != nil {
			return nil, fmt.Errorf("invalid @every duration: %w", err)
		}
		if d < time.Minute {
			return nil, fmt.Errorf("@every must be at least 1 minute")
		}
		return &Schedule{Every: d}, nil
	}
	return parseFields(expr)
}

// Next returns the next fire time at or after t.
func (s *Schedule) Next(t time.Time) time.Time {
	t = t.Add(time.Minute).Truncate(time.Minute)
	if s.Every > 0 {
		return t.Add(s.Every - time.Minute)
	}
	// Brute-force search up to one year ahead; cheap and correct.
	limit := t.Add(366 * 24 * time.Hour)
	for cur := t; cur.Before(limit); cur = cur.Add(time.Minute) {
		if matches(cur.Minute(), s.Minutes) &&
			matches(cur.Hour(), s.Hours) &&
			matches(cur.Day(), s.Days) &&
			matches(int(cur.Month()), s.Months) &&
			matches(int(cur.Weekday()), s.Weekdays) {
			return cur
		}
	}
	return time.Time{}
}

func matches(v int, allowed []int) bool {
	for _, x := range allowed {
		if x == v {
			return true
		}
	}
	return false
}

func parseFields(expr string) (*Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("expected 5 cron fields (minute hour day month weekday), got %d", len(parts))
	}
	mins, err := parseField(parts[0], 0, 59, nil)
	if err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	hrs, err := parseField(parts[1], 0, 23, nil)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	days, err := parseField(parts[2], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("day: %w", err)
	}
	months, err := parseField(parts[3], 1, 12, nil)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	wdays, err := parseField(parts[4], 0, 6, dowAliases)
	if err != nil {
		return nil, fmt.Errorf("weekday: %w", err)
	}
	return &Schedule{Minutes: mins, Hours: hrs, Days: days, Months: months, Weekdays: wdays}, nil
}

var dowAliases = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

func parseField(field string, lo, hi int, aliases map[string]int) ([]int, error) {
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "*" {
		out := make([]int, 0, hi-lo+1)
		for i := lo; i <= hi; i++ {
			out = append(out, i)
		}
		return out, nil
	}
	var out []int
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		// Step form: */N
		if strings.HasPrefix(part, "*/") {
			step, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step %q", part)
			}
			for i := lo; i <= hi; i += step {
				out = append(out, i)
			}
			continue
		}
		// Range form: N-N
		if i := strings.Index(part, "-"); i >= 0 {
			a, errA := parseAtom(part[:i], aliases)
			b, errB := parseAtom(part[i+1:], aliases)
			if errA != nil || errB != nil || a < lo || b > hi || a > b {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			for v := a; v <= b; v++ {
				out = append(out, v)
			}
			continue
		}
		// Single value.
		v, err := parseAtom(part, aliases)
		if err != nil || v < lo || v > hi {
			return nil, fmt.Errorf("invalid value %q", part)
		}
		out = append(out, v)
	}
	return out, nil
}

func parseAtom(s string, aliases map[string]int) (int, error) {
	s = strings.TrimSpace(s)
	if aliases != nil {
		if v, ok := aliases[s]; ok {
			return v, nil
		}
	}
	return strconv.Atoi(s)
}
