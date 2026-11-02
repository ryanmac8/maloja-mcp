package maloja

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL+"/", "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewRejectsBadURL(t *testing.T) {
	for _, u := range []string{"", "maloja:42010", "ftp://x"} {
		if _, err := New(u, "", nil); err == nil {
			t.Errorf("New(%q) should fail", u)
		}
	}
}

func TestScrobblesRequestAndDecode(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		_, _ = w.Write([]byte(`{"status":"ok","list":[{"time":1700000000,"track":{"artists":["A","B"],"title":"T","album":{"artists":["A"],"albumtitle":"Al"},"length":null},"duration":180,"origin":"client:x"}]}`))
	})
	list, err := c.Scrobbles(context.Background(), url.Values{"artist": {"A"}, "perpage": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/apis/mlj_1/scrobbles" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery.Get("artist") != "A" || gotQuery.Get("perpage") != "1" || gotQuery.Has("key") {
		t.Errorf("query = %v", gotQuery)
	}
	if len(list) != 1 || list[0].Track.Title != "T" || list[0].Track.Album.Title != "Al" || *list[0].Duration != 180 {
		t.Errorf("decoded = %+v", list)
	}
}

func TestAPIKeyIsSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "sekrit" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","amount":7}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "sekrit", srv.Client())
	n, err := c.NumScrobbles(context.Background(), nil)
	if err != nil || n != 7 {
		t.Fatalf("NumScrobbles = %d, %v", n, err)
	}
}

func TestErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/mlj_1/artistinfo":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","error":{"type":"entity_does_not_exist","value":"Nobody","desc":"This entity does not exist in the database."}}`))
		case "/apis/mlj_1/numscrobbles":
			// Maloja answers some bad input with HTTP 200 and status=failure.
			_, _ = w.Write([]byte(`{"status":"failure","error":{"type":"unknown_error","value":"IndexError","desc":"The server has encountered an exception."}}`))
		default:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`<html>bad gateway</html>`))
		}
	})
	ctx := context.Background()

	_, err := c.ArtistInfo(ctx, "Nobody")
	if !IsNotFound(err) {
		t.Errorf("ArtistInfo err = %v, want not found", err)
	}
	if _, err := c.NumScrobbles(ctx, nil); err == nil || IsNotFound(err) {
		t.Errorf("NumScrobbles err = %v, want generic API error", err)
	}
	if _, err := c.ServerInfo(ctx); err == nil {
		t.Error("ServerInfo should fail on 502")
	}
	if _, err := c.Top(ctx, "genres", nil); err == nil {
		t.Error("Top with unknown kind should fail")
	}
}

func TestMedalYears(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"artist":"X","scrobbles":3,"position":1,"associated":[],"medals":{"gold":["2019",2020],"silver":[],"bronze":["2021"]},"topweeks":2}`))
	})
	info, err := c.ArtistInfo(context.Background(), "X")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Medals.Gold) != 2 || info.Medals.Gold[0] != 2019 || info.Medals.Gold[1] != 2020 || info.Medals.Bronze[0] != 2021 {
		t.Errorf("medals = %+v", info.Medals)
	}
}

func TestTrackInfoQuery(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("title") != "Rain" || q["trackartist"][0] != "FISHER" || q.Has("artist") {
			t.Errorf("trackinfo query = %v", q)
		}
		_, _ = w.Write([]byte(`{"track":{"artists":["FISHER"],"title":"Rain"},"scrobbles":50,"position":3,"medals":{"gold":[],"silver":[],"bronze":[]},"topweeks":0}`))
	})
	info, err := c.TrackInfo(context.Background(), []string{"FISHER"}, "Rain")
	if err != nil || info.Scrobbles != 50 {
		t.Fatalf("TrackInfo = %+v, %v", info, err)
	}
}
