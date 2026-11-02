package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanmac8/maloja-mcp/internal/maloja"
)

// fakeMaloja records requests and answers with canned JSON per endpoint.
type fakeMaloja struct {
	mu       sync.Mutex
	requests []*url.URL
	answer   func(path string, q url.Values) (int, string)
}

func (f *fakeMaloja) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL)
	f.mu.Unlock()
	status, body := f.answer(strings.TrimPrefix(r.URL.Path, "/apis/mlj_1/"), r.URL.Query())
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (f *fakeMaloja) lastQuery(path string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if strings.TrimPrefix(f.requests[i].Path, "/apis/mlj_1/") == path {
			return f.requests[i].Query()
		}
	}
	return nil
}

func setup(t *testing.T, answer func(path string, q url.Values) (int, string)) (*mcp.ClientSession, *fakeMaloja) {
	t.Helper()
	fake := &fakeMaloja{answer: answer}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	client, err := maloja.New(srv.URL, "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, &Deps{Client: client, Location: ny, Now: func() time.Time {
		return time.Date(2026, 11, 2, 9, 0, 0, 0, ny)
	}})

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, fake
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, bool, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		return nil, true, text
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s returned non-JSON %q: %v", name, text, err)
	}
	return out, false, text
}

const scrobbleJSON = `{"status":"ok","list":[{"time":1793591484,"track":{"artists":["A"],"title":"T","album":{"artists":["A"],"albumtitle":"Al"},"length":200},"duration":199,"origin":"client:x"}]}`

func TestListTools(t *testing.T) {
	cs, _ := setup(t, func(string, url.Values) (int, string) { return 200, `{}` })
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"get_recent_scrobbles": true, "count_scrobbles": true, "get_top_artists": true, "get_top_tracks": true,
		"get_top_albums": true, "get_listening_summary": true, "get_listening_over_time": true, "get_number_ones": true,
		"get_artist_info": true, "get_track_info": true, "get_album_info": true, "search_library": true,
	}
	for _, tool := range res.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
		delete(want, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q must be annotated read-only", tool.Name)
		}
	}
	for name := range want {
		t.Errorf("missing tool %q", name)
	}
}

func TestRecentScrobbles(t *testing.T) {
	cs, fake := setup(t, func(path string, q url.Values) (int, string) { return 200, scrobbleJSON })

	out, isErr, text := call(t, cs, "get_recent_scrobbles", map[string]any{"artist": "A", "track": "T", "period": "last_month", "limit": 1, "oldest_first": true})
	if isErr {
		t.Fatal(text)
	}
	q := fake.lastQuery("scrobbles")
	if q.Get("title") != "T" || q.Get("trackartist") != "A" || q.Has("artist") {
		t.Errorf("track filter query = %v", q)
	}
	if q.Get("from") != "2026/10/01" || q.Get("to") != "2026/10/31" || q.Get("perpage") != "1" || q.Get("reverse") != "no" {
		t.Errorf("range/paging query = %v", q)
	}
	if out["has_more"] != true {
		t.Errorf("has_more = %v", out["has_more"])
	}
	s := out["scrobbles"].([]any)[0].(map[string]any)
	if s["time"] != "2026-11-01 22:51 EST" || s["title"] != "T" || s["album"] != "Al" {
		t.Errorf("scrobble = %v", s)
	}
}

func TestInputValidation(t *testing.T) {
	cs, _ := setup(t, func(string, url.Values) (int, string) { return 200, scrobbleJSON })
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"get_recent_scrobbles", map[string]any{"track": "T"}, "needs artist"},
		{"get_recent_scrobbles", map[string]any{"artist": "A", "track": "T", "album": "Al"}, "not both"},
		{"count_scrobbles", map[string]any{"period": "fortnight"}, "unrecognised period"},
		{"get_listening_over_time", map[string]any{"step": "hour"}, "step must be"},
		{"get_number_ones", map[string]any{"kind": "genre"}, "kind must be"},
		{"get_number_ones", map[string]any{"step": "day"}, "step must be"},
		{"get_artist_info", map[string]any{"artist": " "}, "required"},
		{"search_library", map[string]any{"query": ""}, "required"},
	}
	for _, c := range cases {
		_, isErr, text := call(t, cs, c.tool, c.args)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s(%v) = %q (isError=%v), want error containing %q", c.tool, c.args, text, isErr, c.want)
		}
	}
}

func TestNotFoundHint(t *testing.T) {
	cs, _ := setup(t, func(string, url.Values) (int, string) {
		return 404, `{"status":"error","error":{"type":"entity_does_not_exist","value":"Nobody","desc":"This entity does not exist in the database."}}`
	})
	_, isErr, text := call(t, cs, "get_artist_info", map[string]any{"artist": "Nobody"})
	if !isErr || !strings.Contains(text, "search_library") {
		t.Errorf("got %q", text)
	}
}

func TestTopArtistsTruncates(t *testing.T) {
	cs, fake := setup(t, func(string, url.Values) (int, string) {
		return 200, `{"status":"ok","list":[{"rank":1,"artist":"A","scrobbles":9},{"rank":2,"artist":"B","scrobbles":5},{"rank":3,"artist":"C","scrobbles":1}]}`
	})
	out, isErr, text := call(t, cs, "get_top_artists", map[string]any{"period": "2024", "limit": 2})
	if isErr {
		t.Fatal(text)
	}
	if fake.lastQuery("charts/artists").Get("in") != "2024" {
		t.Errorf("query = %v", fake.lastQuery("charts/artists"))
	}
	if len(out["artists"].([]any)) != 2 || out["distinct_artists"].(float64) != 3 {
		t.Errorf("out = %v", out)
	}
}

func TestSummary(t *testing.T) {
	cs, _ := setup(t, func(path string, q url.Values) (int, string) {
		switch path {
		case "numscrobbles":
			return 200, `{"status":"ok","amount":42}`
		case "charts/artists":
			return 200, `{"status":"ok","list":[{"rank":1,"artist":"A","scrobbles":9}]}`
		case "charts/tracks":
			return 200, `{"status":"ok","list":[{"rank":1,"track":{"artists":["A"],"title":"T"},"scrobbles":4}]}`
		case "charts/albums":
			return 200, `{"status":"ok","list":[{"rank":1,"album":{"artists":["A"],"albumtitle":"Al"},"scrobbles":4}]}`
		}
		return 500, ``
	})
	out, isErr, text := call(t, cs, "get_listening_summary", map[string]any{"period": "this_week"})
	if isErr {
		t.Fatal(text)
	}
	if out["total_scrobbles"].(float64) != 42 || out["period"] != "2026/11/02 to 2026/11/08" {
		t.Errorf("out = %v", out)
	}
	tr := out["top_tracks"].([]any)[0].(map[string]any)
	if tr["title"] != "T" || tr["rank"].(float64) != 1 {
		t.Errorf("track row = %v", tr)
	}
}

func TestOverTimeDefaultsAndNumberOnes(t *testing.T) {
	cs, fake := setup(t, func(path string, q url.Values) (int, string) {
		switch path {
		case "pulse":
			return 200, `{"status":"ok","list":[{"range":{"description":"October 2026"},"scrobbles":10}]}`
		case "top/tracks":
			return 200, `{"status":"ok","list":[{"range":{"description":"2025"},"track":{"artists":["A"],"title":"T"},"scrobbles":3},{"range":{"description":"2026"},"track":null,"scrobbles":0}]}`
		}
		return 500, ``
	})
	if _, isErr, text := call(t, cs, "get_listening_over_time", map[string]any{"step": "day"}); isErr {
		t.Fatal(text)
	}
	q := fake.lastQuery("pulse")
	if q.Get("step") != "day" || q.Get("from") != "2026/10/04" || q.Get("to") != "2026/11/02" {
		t.Errorf("pulse query = %v", q)
	}

	out, isErr, text := call(t, cs, "get_number_ones", map[string]any{"kind": "song"})
	if isErr {
		t.Fatal(text)
	}
	if q := fake.lastQuery("top/tracks"); q.Get("step") != "year" || q.Has("in") || q.Has("from") {
		t.Errorf("top query = %v", q)
	}
	rows := out["number_ones"].([]any)
	if len(rows) != 1 {
		t.Errorf("empty steps should be skipped: %v", rows)
	}
}

func TestArtistInfoFirstLast(t *testing.T) {
	cs, _ := setup(t, func(path string, q url.Values) (int, string) {
		switch path {
		case "artistinfo":
			return 200, `{"artist":"A","scrobbles":3,"position":5,"associated":[],"medals":{"gold":["2024"],"silver":[],"bronze":[]},"topweeks":1}`
		case "scrobbles":
			if q.Get("reverse") == "no" {
				return 200, `{"status":"ok","list":[{"time":1420070400,"track":{"artists":["A"],"title":"Old"}}]}`
			}
			return 200, scrobbleJSON
		case "charts/tracks":
			return 200, `{"status":"ok","list":[{"rank":1,"track":{"artists":["A"],"title":"T"},"scrobbles":3}]}`
		}
		return 500, ``
	})
	out, isErr, text := call(t, cs, "get_artist_info", map[string]any{"artist": "A"})
	if isErr {
		t.Fatal(text)
	}
	if out["first_played"] != "2014-12-31 19:00 EST" || out["last_played"] != "2026-11-01 22:51 EST" {
		t.Errorf("first/last = %v / %v", out["first_played"], out["last_played"])
	}
	if out["yearly_medals"].(map[string]any)["gold"].([]any)[0].(float64) != 2024 {
		t.Errorf("medals = %v", out["yearly_medals"])
	}
}
