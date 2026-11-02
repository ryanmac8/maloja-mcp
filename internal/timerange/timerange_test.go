package timerange

import (
	"net/url"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// Monday 2026-11-02, 00:30 local: just after midnight to catch UTC slips.
	now := time.Date(2026, 11, 2, 0, 30, 0, 0, ny)

	tests := []struct {
		period, from, to string
		want             Range
	}{
		{"", "", "", Range{}},
		{"all_time", "", "", Range{}},
		{"today", "", "", Range{From: "2026/11/02", To: "2026/11/02"}},
		{"yesterday", "", "", Range{From: "2026/11/01", To: "2026/11/01"}},
		{"this_week", "", "", Range{From: "2026/11/02", To: "2026/11/08"}},
		{"last_week", "", "", Range{From: "2026/10/26", To: "2026/11/01"}},
		{"this_month", "", "", Range{From: "2026/11/01", To: "2026/11/30"}},
		{"last_month", "", "", Range{From: "2026/10/01", To: "2026/10/31"}},
		{"lastmonth", "", "", Range{From: "2026/10/01", To: "2026/10/31"}},
		{"Last Month", "", "", Range{From: "2026/10/01", To: "2026/10/31"}},
		{"this_year", "", "", Range{In: "2026"}},
		{"last_year", "", "", Range{In: "2025"}},
		{"last_7_days", "", "", Range{From: "2026/10/27", To: "2026/11/02"}},
		{"last_30_days", "", "", Range{From: "2026/10/04", To: "2026/11/02"}},
		{"2024", "", "", Range{In: "2024"}},
		{"2024-08", "", "", Range{In: "2024/08"}},
		{"2024/8", "", "", Range{In: "2024/08"}},
		{"2024-08-15", "", "", Range{From: "2024/08/15", To: "2024/08/15"}},
		{"2024-W42", "", "", Range{From: "2024/10/14", To: "2024/10/20"}},
		{"2026-w01", "", "", Range{From: "2025/12/29", To: "2026/01/04"}},
		{"august", "", "", Range{From: "2026/08/01", To: "2026/08/31"}},
		{"december", "", "", Range{From: "2025/12/01", To: "2025/12/31"}},
		{"nov", "", "", Range{From: "2026/11/01", To: "2026/11/30"}},
		{"", "2024-01-05", "", Range{From: "2024/01/05"}},
		{"", "", "2024-1-5", Range{To: "2024/01/05"}},
		{"", "2024-01-01", "2024-03-31", Range{From: "2024/01/01", To: "2024/03/31"}},
		{"2024", "2020-01-01", "", Range{In: "2024"}}, // period wins
	}
	for _, tt := range tests {
		got, err := Resolve(tt.period, tt.from, tt.to, now)
		if err != nil {
			t.Errorf("Resolve(%q,%q,%q): unexpected error %v", tt.period, tt.from, tt.to, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Resolve(%q,%q,%q) = %+v, want %+v", tt.period, tt.from, tt.to, got, tt.want)
		}
	}
}

func TestResolveErrors(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	bad := []struct{ period, from, to string }{
		{"lastfortnight", "", ""},
		{"2024-13", "", ""},
		{"2024-W60", "", ""},
		{"2024-02-30", "", ""},
		{"last_0_days", "", ""},
		{"", "yesterday", ""},
		{"", "2024-05-01", "2024-01-01"},
	}
	for _, b := range bad {
		if r, err := Resolve(b.period, b.from, b.to, now); err == nil {
			t.Errorf("Resolve(%q,%q,%q) = %+v, want error", b.period, b.from, b.to, r)
		}
	}
}

func TestApplyAndString(t *testing.T) {
	q := url.Values{}
	Range{In: "2024"}.Apply(q)
	if q.Get("in") != "2024" || q.Has("from") {
		t.Errorf("in range applied wrongly: %v", q)
	}
	q = url.Values{}
	Range{From: "2024/01/01", To: "2024/01/31"}.Apply(q)
	if q.Get("from") != "2024/01/01" || q.Get("to") != "2024/01/31" {
		t.Errorf("from/to applied wrongly: %v", q)
	}
	q = url.Values{}
	Range{}.Apply(q)
	if len(q) != 0 {
		t.Errorf("zero range should add nothing: %v", q)
	}

	for r, want := range map[Range]string{
		{}:                                     "all time",
		{In: "2024"}:                           "2024",
		{From: "2024/01/01", To: "2024/01/01"}: "2024/01/01",
		{From: "2024/01/01", To: "2024/02/01"}: "2024/01/01 to 2024/02/01",
		{From: "2024/01/01"}:                   "since 2024/01/01",
		{To: "2024/01/01"}:                     "until 2024/01/01",
	} {
		if got := r.String(); got != want {
			t.Errorf("%+v.String() = %q, want %q", r, got, want)
		}
	}
}
