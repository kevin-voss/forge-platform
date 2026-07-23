package schedule_test

import (
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/schedule"
)

func TestCronMatchesBusinessHours(t *testing.T) {
	c, err := schedule.ParseCron("* 7-19 * * MON-FRI")
	if err != nil {
		t.Fatal(err)
	}
	thuNoon := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	if !c.Matches(thuNoon) {
		t.Fatal("expected Thursday noon to match")
	}
	thuNight := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	if c.Matches(thuNight) {
		t.Fatal("expected Thursday 20:00 not to match")
	}
	satNoon := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	if c.Matches(satNoon) {
		t.Fatal("expected Saturday not to match")
	}
}

func TestCronRejectsInvalid(t *testing.T) {
	if _, err := schedule.ParseCron("not a cron"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := schedule.LoadLocation("Not/A_Zone"); err == nil {
		t.Fatal("expected timezone error")
	}
}

func TestActiveBoundsMergeAndConflict(t *testing.T) {
	minA, maxA := 10, 30
	minB, maxB := 4, 12
	schedules := []schedule.Spec{
		{Name: "peak", Cron: "* * * * *", MinReplicas: &minA, MaxReplicas: &maxA},
		{Name: "cap", Cron: "* * * * *", MinReplicas: &minB, MaxReplicas: &maxB},
	}
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	b := schedule.ActiveBounds(2, 40, schedules, now)
	if b.Conflict {
		t.Fatalf("unexpected conflict: %s", b.ConflictMessage)
	}
	if b.Min != 10 || b.Max != 12 {
		t.Fatalf("expected merged 10/12, got %d/%d", b.Min, b.Max)
	}
	if len(b.Active) != 2 {
		t.Fatalf("expected 2 active, got %v", b.Active)
	}

	badMax := 5
	conflict := []schedule.Spec{
		{Name: "hi", Cron: "* * * * *", MinReplicas: &minA},
		{Name: "lo", Cron: "* * * * *", MaxReplicas: &badMax},
	}
	c := schedule.ActiveBounds(2, 40, conflict, now)
	if !c.Conflict {
		t.Fatal("expected ScheduleConflict")
	}
	if c.Min != 2 || c.Max != 40 {
		t.Fatalf("conflict should fall back to base, got %d/%d", c.Min, c.Max)
	}
}

func TestScheduleEndTimeCutsOff(t *testing.T) {
	min := 8
	sch := schedule.Spec{
		Name:        "promo",
		Cron:        "* * * * *",
		MinReplicas: &min,
		EndTime:     "2026-07-23T11:00:00Z",
	}
	before := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	ok, err := schedule.IsActive(sch, before)
	if err != nil || !ok {
		t.Fatalf("expected active before endTime, ok=%v err=%v", ok, err)
	}
	after := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	ok, err = schedule.IsActive(sch, after)
	if err != nil || ok {
		t.Fatalf("expected inactive after endTime, ok=%v err=%v", ok, err)
	}
}

func TestTimezoneShift(t *testing.T) {
	min := 6
	sch := schedule.Spec{
		Name:        "ny-open",
		Cron:        "0 9 * * *",
		TimeZone:    "America/New_York",
		MinReplicas: &min,
	}
	at := time.Date(2026, 7, 23, 13, 0, 0, 0, time.UTC)
	ok, err := schedule.IsActive(sch, at)
	if err != nil || !ok {
		t.Fatalf("expected active at 09:00 America/New_York, ok=%v err=%v", ok, err)
	}
}
