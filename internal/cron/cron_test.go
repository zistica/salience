package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Schedule {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("time parse %q: %v", s, err)
	}
	return v
}

func TestParse_Aliases(t *testing.T) {
	cases := []string{"@hourly", "@daily", "@weekly"}
	for _, c := range cases {
		if _, err := Parse(c); err != nil {
			t.Errorf("Parse(%q) returned error: %v", c, err)
		}
	}
}

func TestParse_Every(t *testing.T) {
	s, err := Parse("@every 5m")
	if err != nil {
		t.Fatalf("Parse(@every 5m): %v", err)
	}
	if s.Every != 5*time.Minute {
		t.Errorf("expected 5m, got %v", s.Every)
	}
	now := mustTime(t, "2026-05-20T10:00:00Z")
	next := s.Next(now)
	if next.Sub(now) > 5*time.Minute+time.Second || next.Sub(now) < 4*time.Minute {
		t.Errorf("Next() should land ~5m later, got %v", next.Sub(now))
	}
}

func TestParse_EveryTooShort(t *testing.T) {
	if _, err := Parse("@every 10s"); err == nil {
		t.Error("expected @every 10s to be rejected (< 1 minute)")
	}
}

func TestParse_FullCronFields(t *testing.T) {
	s := mustParse(t, "0 9 * * 1")
	now := mustTime(t, "2026-05-20T15:00:00Z") // Wed
	next := s.Next(now)
	// Next Monday at 09:00.
	if next.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s", next.Weekday())
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("expected 09:00, got %02d:%02d", next.Hour(), next.Minute())
	}
}

func TestParse_RangesAndLists(t *testing.T) {
	s := mustParse(t, "0 9-17 * * mon-fri")
	now := mustTime(t, "2026-05-22T03:00:00Z") // Fri
	next := s.Next(now)
	if next.Hour() < 9 || next.Hour() > 17 {
		t.Errorf("range 9-17 should constrain hour, got %d", next.Hour())
	}
}

func TestParse_StepValues(t *testing.T) {
	s := mustParse(t, "*/15 * * * *")
	now := mustTime(t, "2026-05-20T10:07:00Z")
	next := s.Next(now)
	if next.Minute()%15 != 0 {
		t.Errorf("expected minute aligned to 15-min step, got %d", next.Minute())
	}
}

func TestParse_DayOfWeekAliases(t *testing.T) {
	// Both forms should produce identical Schedules.
	a := mustParse(t, "0 8 * * MON")
	b := mustParse(t, "0 8 * * 1")
	if len(a.Weekdays) != len(b.Weekdays) || a.Weekdays[0] != b.Weekdays[0] {
		t.Errorf("MON alias should equal numeric 1, got %v vs %v", a.Weekdays, b.Weekdays)
	}
}

func TestParse_Invalid(t *testing.T) {
	cases := []string{"", "* * *", "bogus", "60 * * * *", "* 24 * * *"}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q) should have errored", c)
		}
	}
}

func TestNext_MovesForwardEvenOnExactMatch(t *testing.T) {
	// A schedule that matches "right now" should still return the NEXT
	// fire, not the current minute (otherwise the ticker would re-fire
	// instantly in a loop).
	s := mustParse(t, "0 12 * * *")
	now := mustTime(t, "2026-05-20T12:00:30Z")
	next := s.Next(now)
	if !next.After(now) {
		t.Errorf("Next() should be strictly after now, got %v vs %v", next, now)
	}
}
