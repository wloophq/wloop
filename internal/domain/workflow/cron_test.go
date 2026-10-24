package workflow_test

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestCronNextAndPrev(t *testing.T) {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, loc) }

	weekdays, err := workflow.ParseCron("0 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		got, want time.Time
	}{
		{"next after friday", weekdays.Next(at(2026, 10, 23, 10, 0)), at(2026, 10, 26, 9, 0)},
		{"next is strictly after", weekdays.Next(at(2026, 10, 26, 9, 0)), at(2026, 10, 27, 9, 0)},
		{"prev on sunday", weekdays.Prev(at(2026, 10, 25, 12, 0)), at(2026, 10, 23, 9, 0)},
		{"prev includes the instant", weekdays.Prev(at(2026, 10, 26, 9, 0)), at(2026, 10, 26, 9, 0)},
	}
	quarter, err := workflow.ParseCron("*/15 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases,
		struct {
			name      string
			got, want time.Time
		}{"every 15 next", quarter.Next(at(2026, 10, 23, 10, 7)), at(2026, 10, 23, 10, 15)},
		struct {
			name      string
			got, want time.Time
		}{"every 15 across the hour", quarter.Next(at(2026, 10, 23, 10, 50)), at(2026, 10, 23, 11, 0)},
		struct {
			name      string
			got, want time.Time
		}{"every 15 prev", quarter.Prev(at(2026, 10, 23, 10, 14)), at(2026, 10, 23, 10, 0)},
	)
	for _, c := range cases {
		if !c.got.Equal(c.want) {
			t.Errorf("%s: %v, want %v", c.name, c.got, c.want)
		}
	}

	if _, err := workflow.ParseCron("61 * * * *"); err == nil || err.Error() != "minute 61 out of range 0-59" {
		t.Errorf("ParseCron(61 ...) err = %v", err)
	}
	for _, bad := range []string{"* * * *", "a * * * *", "*/0 * * * *", "5-1 * * * *", "* * 0 * *", "* * * 13 *", "* * * * 8"} {
		if _, err := workflow.ParseCron(bad); err == nil {
			t.Errorf("ParseCron(%q) parsed", bad)
		}
	}
}

func TestCronDayOfMonthOrDayOfWeek(t *testing.T) {
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }
	wednesdays, err := workflow.ParseCron("0 0 1 * 3")
	if err != nil {
		t.Fatal(err)
	}
	if got := wednesdays.Next(day(10, 22)); !got.Equal(day(10, 28)) {
		t.Errorf("Next = %v, want the wednesday", got)
	}
	if got := wednesdays.Next(day(10, 28)); !got.Equal(day(11, 1)) {
		t.Errorf("Next = %v, want the 1st, a sunday", got)
	}
	sundays, err := workflow.ParseCron("0 0 * * 7")
	if err != nil {
		t.Fatal(err)
	}
	if got := sundays.Next(day(10, 22)); !got.Equal(day(10, 25)) {
		t.Errorf("Next = %v, want sunday as 7", got)
	}
}

func TestCronCrossesDSTChanges(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at := func(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, loc) }
	three, err := workflow.ParseCron("0 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	halfPastTwo, err := workflow.ParseCron("30 2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	secondHalfPastOne := at(11, 1, 1, 30).Add(time.Hour)
	cases := []struct {
		name string
		call func() time.Time
		want time.Time
	}{
		{"next before the fall-back", func() time.Time { return three.Next(at(11, 1, 0, 30)) }, at(11, 1, 3, 0)},
		{"next inside the repeated hour", func() time.Time { return three.Next(at(11, 1, 1, 30)) }, at(11, 1, 3, 0)},
		{"next inside the second repeated hour", func() time.Time { return three.Next(secondHalfPastOne) }, at(11, 1, 3, 0)},
		{"prev after the fall-back", func() time.Time { return three.Prev(at(11, 1, 4, 0)) }, at(11, 1, 3, 0)},
		{"prev inside the repeated hour", func() time.Time { return three.Prev(secondHalfPastOne) }, at(10, 31, 3, 0)},
		{"next across the spring-forward", func() time.Time { return three.Next(at(3, 8, 0, 30)) }, at(3, 8, 3, 0)},
		{"prev across the spring-forward", func() time.Time { return three.Prev(at(3, 8, 4, 0)) }, at(3, 8, 3, 0)},
		{"next skips a time the spring-forward removes", func() time.Time { return halfPastTwo.Next(at(3, 8, 0, 0)) }, at(3, 9, 2, 30)},
		{"prev skips a time the spring-forward removes", func() time.Time { return halfPastTwo.Prev(at(3, 8, 23, 0)) }, at(3, 7, 2, 30)},
	}
	for _, c := range cases {
		done := make(chan time.Time, 1)
		go func() { done <- c.call() }()
		select {
		case got := <-done:
			if !got.Equal(c.want) {
				t.Errorf("%s: %v, want %v", c.name, got, c.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: did not return", c.name)
		}
	}
}
