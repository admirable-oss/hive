package workflow

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a cron line: minute, hour, day of the month, month and day
// of the week, in local time.
//
//	*/15 * * * *        every 15 minutes
//	0 3 * * 1-5         03:00 on weekdays
//	30 9 1,15 * *       09:30 on the 1st and the 15th
//	0 0 * jan,jul sun   midnight on Sundays in January and July
//
// Each field is *, a value, a range (a-b), a list (a,b), and any of these
// with a step (*/5, 10-40/10). Months and days may be named (jan, mon).
// Day of the week counts from Sunday, 0 (7 is Sunday too). When both day
// fields are restricted, either matching is enough, as cron has it.
// @hourly, @daily, @weekly, @monthly and @yearly are shorthands.
type Schedule struct {
	minute, hour, dom, month, dow uint64 // bit sets
	domAny, dowAny                bool
}

var cronShorthands = map[string]string{
	"@hourly": "0 * * * *", "@daily": "0 0 * * *", "@midnight": "0 0 * * *",
	"@weekly": "0 0 * * 0", "@monthly": "0 0 1 * *", "@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *",
}

var monthNames = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}

var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseSchedule reads a cron line.
func ParseSchedule(line string) (Schedule, error) {
	if s, ok := cronShorthands[strings.ToLower(strings.TrimSpace(line))]; ok {
		line = s
	}
	fields := strings.Fields(line)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("schedule %q: want 5 fields (minute hour day-of-month month day-of-week), have %d", line, len(fields))
	}
	var s Schedule
	var err error
	if s.minute, err = cronField(fields[0], 0, 59, nil, 0); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: minute: %w", line, err)
	}
	if s.hour, err = cronField(fields[1], 0, 23, nil, 0); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: hour: %w", line, err)
	}
	if s.dom, err = cronField(fields[2], 1, 31, nil, 0); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: day of the month: %w", line, err)
	}
	if s.month, err = cronField(fields[3], 1, 12, monthNames, 1); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: month: %w", line, err)
	}
	if s.dow, err = cronField(fields[4], 0, 7, dayNames, 0); err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: day of the week: %w", line, err)
	}
	if s.dow&(1<<7) != 0 { // 7 is Sunday
		s.dow |= 1
	}
	s.domAny = strings.HasPrefix(fields[2], "*")
	s.dowAny = strings.HasPrefix(fields[4], "*")
	return s, nil
}

// cronField reads one field into a bit set of the values in [lo, hi].
// names, when given, name the values from base.
func cronField(f string, lo, hi int, names []string, base int) (uint64, error) {
	var set uint64
	for part := range strings.SplitSeq(f, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("step %q", stepText)
			}
			step = n
		}
		from, to := lo, hi
		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if from, err = cronValue(a, lo, hi, names, base); err != nil {
				return 0, err
			}
			if to, err = cronValue(b, lo, hi, names, base); err != nil {
				return 0, err
			}
			if from > to {
				return 0, fmt.Errorf("range %q runs backwards", rng)
			}
		default:
			v, err := cronValue(rng, lo, hi, names, base)
			if err != nil {
				return 0, err
			}
			from = v
			if !hasStep {
				to = v
			}
		}
		for v := from; v <= to; v += step {
			set |= 1 << v
		}
	}
	if set == 0 {
		return 0, fmt.Errorf("%q selects nothing", f)
	}
	return set, nil
}

func cronValue(s string, lo, hi int, names []string, base int) (int, error) {
	for i, n := range names {
		if strings.EqualFold(s, n) {
			return i + base, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < lo || v > hi {
		return 0, fmt.Errorf("%q is not between %d and %d", s, lo, hi)
	}
	return v, nil
}

// Next returns the first time after t that the schedule fires, at the
// start of its minute; the zero time when none comes within five years
// (February 30th).
func (s Schedule) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if s.month&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if s.hour&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if s.minute&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (s Schedule) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<uint(t.Day())) != 0
	dow := s.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case s.domAny && s.dowAny:
		return true
	case s.domAny:
		return dow
	case s.dowAny:
		return dom
	}
	return dom || dow
}
