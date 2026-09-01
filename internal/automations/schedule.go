package automations

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSpec is a parsed standard 5-field cron expression
// (minute hour day-of-month month day-of-week).
type cronSpec struct {
	minute map[int]bool
	hour   map[int]bool
	dom    map[int]bool
	month  map[int]bool
	dow    map[int]bool
	domAll bool
	dowAll bool
}

var (
	monthNames = map[string]int{
		"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
		"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
	}
	dayNames = map[string]int{
		"SUN": 0, "MON": 1, "TUE": 2, "WED": 3,
		"THU": 4, "FRI": 5, "SAT": 6,
	}
)

// parseCron parses the supported subset: *, */n, a-b, a,b (mixed),
// numeric months/weeks and JAN-DEC / SUN-SAT names. L, W, #, ? and
// seconds are rejected.
func parseCron(expr string) (*cronSpec, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}
	minute, err := parseCronField(fields[0], 0, 59, nil)
	if err != nil {
		return nil, fmt.Errorf("cron: minute: %w", err)
	}
	hour, err := parseCronField(fields[1], 0, 23, nil)
	if err != nil {
		return nil, fmt.Errorf("cron: hour: %w", err)
	}
	dom, err := parseCronField(fields[2], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("cron: day of month: %w", err)
	}
	month, err := parseCronField(fields[3], 1, 12, monthNames)
	if err != nil {
		return nil, fmt.Errorf("cron: month: %w", err)
	}
	dow, err := parseCronField(fields[4], 0, 7, dayNames)
	if err != nil {
		return nil, fmt.Errorf("cron: day of week: %w", err)
	}
	// 0 and 7 are both Sunday in standard cron.
	if dow[7] {
		dow[0] = true
	}
	delete(dow, 7)
	return &cronSpec{
		minute: minute,
		hour:   hour,
		dom:    dom,
		month:  month,
		dow:    dow,
		domAll: fields[2] == "*",
		dowAll: fields[4] == "*",
	}, nil
}

func parseCronField(s string, min, max int, names map[string]int) (map[int]bool, error) {
	set := make(map[int]bool)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty element in %q", s)
		}
		switch {
		case part == "*":
			for i := min; i <= max; i++ {
				set[i] = true
			}
		case strings.HasPrefix(part, "*/"):
			n, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
			if err != nil || n < 1 {
				return nil, fmt.Errorf("invalid step %q", part)
			}
			for i := min; i <= max; i += n {
				set[i] = true
			}
		case strings.Contains(part, "-"):
			lo, hi, ok := strings.Cut(part, "-")
			if !ok {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			a, err := cronAtom(lo, min, max, names)
			if err != nil {
				return nil, err
			}
			b, err := cronAtom(hi, min, max, names)
			if err != nil {
				return nil, err
			}
			if a > b {
				return nil, fmt.Errorf("range %q goes backwards", part)
			}
			for i := a; i <= b; i++ {
				set[i] = true
			}
		default:
			v, err := cronAtom(part, min, max, names)
			if err != nil {
				return nil, err
			}
			set[v] = true
		}
	}
	return set, nil
}

func cronAtom(s string, min, max int, names map[string]int) (int, error) {
	upper := strings.ToUpper(strings.TrimSpace(s))
	if names != nil {
		if v, ok := names[upper]; ok {
			if v < min || v > max {
				return 0, fmt.Errorf("value %q out of range %d-%d", s, min, max)
			}
			return v, nil
		}
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("value %d out of range %d-%d", v, min, max)
	}
	return v, nil
}

// next returns the next matching minute strictly after after. The
// search is bounded to five years so impossible expressions (e.g.
// Feb 30) fail instead of looping forever.
func (c *cronSpec) next(after time.Time) (time.Time, error) {
	limit := after.AddDate(5, 0, 0)
	for t := after.Add(time.Minute); t.Before(limit); t = t.Add(time.Minute) {
		if c.match(t) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cron: no next match within five years")
}

func (c *cronSpec) match(t time.Time) bool {
	if !c.minute[t.Minute()] || !c.hour[t.Hour()] || !c.month[int(t.Month())] {
		return false
	}
	domHit := c.dom[t.Day()]
	dowHit := c.dow[int(t.Weekday())]
	switch {
	case c.domAll && c.dowAll:
		return true
	case c.domAll:
		return dowHit
	case c.dowAll:
		return domHit
	default:
		// Standard crontab OR semantics when both day fields are
		// restricted.
		return domHit || dowHit
	}
}
