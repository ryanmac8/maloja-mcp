// Package timerange turns friendly period names ("last_month", "2024-08")
// into the from/to/in query parameters that Maloja's native API understands.
//
// Maloja's own relative keywords are incomplete (it has "thismonth" but no
// "lastmonth", and unknown words crash the request with an IndexError), so
// relative periods are resolved here into explicit calendar dates in the
// configured time zone.
package timerange

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Keywords lists the relative periods accepted by Resolve, for tool descriptions.
var Keywords = []string{
	"today", "yesterday",
	"this_week", "last_week",
	"this_month", "last_month",
	"this_year", "last_year",
	"last_7_days", "last_30_days", "last_90_days", "last_365_days",
	"all_time",
}

// Description is a short help text describing accepted period formats.
const Description = "Time period. Relative: today, yesterday, this_week, last_week, this_month, last_month, " +
	"this_year, last_year, last_7_days, last_30_days, last_90_days, last_365_days, all_time. " +
	"Absolute: a year (2024), month (2024-08), day (2024-08-15), ISO week (2024-W42) or a month name " +
	"(august = the most recent August). Omit for all time. Weeks run Monday to Sunday."

var (
	reYear  = regexp.MustCompile(`^(\d{4})$`)
	reMonth = regexp.MustCompile(`^(\d{4})[-/](\d{1,2})$`)
	reDay   = regexp.MustCompile(`^(\d{4})[-/](\d{1,2})[-/](\d{1,2})$`)
	reWeek  = regexp.MustCompile(`^(\d{4})[-/]?[wW](\d{1,2})$`)
)

var monthNames = map[string]time.Month{}

func init() {
	for m := time.January; m <= time.December; m++ {
		full := strings.ToLower(m.String())
		monthNames[full] = m
		monthNames[full[:3]] = m
	}
	monthNames["sept"] = time.September
}

// Range is a resolved time range ready to be added to a Maloja query.
// A zero Range means "all time".
type Range struct {
	From string // inclusive start, Maloja date syntax (2024/08/01)
	To   string // inclusive end, Maloja date syntax
	In   string // single Maloja range expression (2024, 2024/08, 2024/W42)
}

// IsZero reports whether the range is unbounded.
func (r Range) IsZero() bool { return r.From == "" && r.To == "" && r.In == "" }

// Apply adds the range to Maloja query parameters.
func (r Range) Apply(q url.Values) {
	if r.In != "" {
		q.Set("in", r.In)
		return
	}
	if r.From != "" {
		q.Set("from", r.From)
	}
	if r.To != "" {
		q.Set("to", r.To)
	}
}

// String describes the range for humans.
func (r Range) String() string {
	switch {
	case r.In != "":
		return r.In
	case r.From != "" && r.To != "":
		if r.From == r.To {
			return r.From
		}
		return r.From + " to " + r.To
	case r.From != "":
		return "since " + r.From
	case r.To != "":
		return "until " + r.To
	default:
		return "all time"
	}
}

func day(t time.Time) string { return t.Format("2006/01/02") }

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// Resolve converts period (or explicit from/to dates) into a Range.
// now must carry the user's time zone. If period is set, from/to are ignored.
func Resolve(period, from, to string, now time.Time) (Range, error) {
	period = strings.TrimSpace(strings.ToLower(period))
	if period == "" {
		var r Range
		var err error
		if from != "" {
			if r.From, err = parseDate(from); err != nil {
				return Range{}, fmt.Errorf("from: %w", err)
			}
		}
		if to != "" {
			if r.To, err = parseDate(to); err != nil {
				return Range{}, fmt.Errorf("to: %w", err)
			}
		}
		if r.From != "" && r.To != "" && r.From > r.To {
			return Range{}, fmt.Errorf("from (%s) is after to (%s)", from, to)
		}
		return r, nil
	}
	return resolvePeriod(period, now)
}

func resolvePeriod(p string, now time.Time) (Range, error) {
	today := startOfDay(now)
	p = strings.ReplaceAll(strings.ReplaceAll(p, " ", "_"), "-", "_")
	// Accept the Maloja spellings too (thismonth, lastyear ...).
	switch strings.ReplaceAll(p, "_", "") {
	case "alltime", "all", "ever":
		return Range{}, nil
	case "today":
		return Range{From: day(today), To: day(today)}, nil
	case "yesterday":
		y := today.AddDate(0, 0, -1)
		return Range{From: day(y), To: day(y)}, nil
	case "thisweek", "week":
		start := mondayOf(today)
		return Range{From: day(start), To: day(start.AddDate(0, 0, 6))}, nil
	case "lastweek":
		start := mondayOf(today).AddDate(0, 0, -7)
		return Range{From: day(start), To: day(start.AddDate(0, 0, 6))}, nil
	case "thismonth", "month":
		return monthRange(today.Year(), today.Month(), today.Location()), nil
	case "lastmonth":
		t := time.Date(today.Year(), today.Month()-1, 1, 0, 0, 0, 0, today.Location())
		return monthRange(t.Year(), t.Month(), t.Location()), nil
	case "thisyear", "year":
		return Range{In: fmt.Sprintf("%d", today.Year())}, nil
	case "lastyear":
		return Range{In: fmt.Sprintf("%d", today.Year()-1)}, nil
	}
	var n int
	if _, err := fmt.Sscanf(p, "last_%d_days", &n); err == nil && n > 0 {
		if n > 36500 {
			return Range{}, fmt.Errorf("period %q is too long", p)
		}
		return Range{From: day(today.AddDate(0, 0, -(n - 1))), To: day(today)}, nil
	}

	raw := strings.ReplaceAll(p, "_", "-")
	if m := reYear.FindStringSubmatch(raw); m != nil {
		return Range{In: m[1]}, nil
	}
	if m := reMonth.FindStringSubmatch(raw); m != nil {
		y, mo := atoi(m[1]), atoi(m[2])
		if mo < 1 || mo > 12 {
			return Range{}, fmt.Errorf("invalid month in %q", p)
		}
		return Range{In: fmt.Sprintf("%d/%02d", y, mo)}, nil
	}
	if m := reWeek.FindStringSubmatch(raw); m != nil {
		y, w := atoi(m[1]), atoi(m[2])
		if w < 1 || w > 53 {
			return Range{}, fmt.Errorf("invalid week in %q", p)
		}
		start := isoWeekStart(y, w, today.Location())
		return Range{From: day(start), To: day(start.AddDate(0, 0, 6))}, nil
	}
	if reDay.MatchString(raw) {
		d, err := parseDate(raw)
		if err != nil {
			return Range{}, err
		}
		return Range{From: d, To: d}, nil
	}
	if mo, ok := monthNames[p]; ok {
		y := today.Year()
		if mo > today.Month() {
			y--
		}
		return monthRange(y, mo, today.Location()), nil
	}
	return Range{}, fmt.Errorf("unrecognised period %q; use one of %s, or a date like 2024, 2024-08, 2024-08-15, 2024-W42",
		p, strings.Join(Keywords, ", "))
}

// atoi converts a regexp-validated run of digits.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func monthRange(y int, m time.Month, loc *time.Location) Range {
	start := time.Date(y, m, 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 1, -1)
	return Range{From: day(start), To: day(end)}
}

func mondayOf(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7 // Monday = 0
	return t.AddDate(0, 0, -offset)
}

func isoWeekStart(year, week int, loc *time.Location) time.Time {
	// Jan 4th is always in ISO week 1.
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, loc)
	return mondayOf(jan4).AddDate(0, 0, (week-1)*7)
}

func parseDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	m := reDay.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("invalid date %q, expected YYYY-MM-DD", s)
	}
	t, err := time.Parse("2006-1-2", m[1]+"-"+m[2]+"-"+m[3])
	if err != nil {
		return "", fmt.Errorf("invalid date %q: %w", s, err)
	}
	return day(t), nil
}
