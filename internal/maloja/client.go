// Package maloja is a small read-only client for Maloja's native API
// (/apis/mlj_1). See https://github.com/krateng/maloja/blob/master/API.md.
package maloja

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to a Maloja server.
type Client struct {
	base   *url.URL
	apiKey string
	http   *http.Client
}

// New returns a client for the Maloja server at baseURL (e.g. http://maloja:42010).
// apiKey is optional; Maloja's read endpoints are public by default.
func New(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse MALOJA_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("MALOJA_URL must start with http:// or https://, got %q", baseURL)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{base: u, apiKey: apiKey, http: httpClient}, nil
}

// APIError is an error reported by Maloja itself.
type APIError struct {
	Status int
	Type   string
	Desc   string
	Value  any
}

func (e *APIError) Error() string {
	if e.Type == "" {
		return fmt.Sprintf("maloja returned HTTP %d", e.Status)
	}
	if e.Value != nil {
		return fmt.Sprintf("maloja: %s (%s): %v", e.Desc, e.Type, e.Value)
	}
	return fmt.Sprintf("maloja: %s (%s)", e.Desc, e.Type)
}

// IsNotFound reports whether err means the artist/track/album does not exist.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Type == "entity_does_not_exist"
}

type envelope struct {
	Status string `json:"status"`
	Error  *struct {
		Type  string `json:"type"`
		Value any    `json:"value"`
		Desc  string `json:"desc"`
	} `json:"error"`
}

func (c *Client) get(ctx context.Context, endpoint string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	if c.apiKey != "" {
		q.Set("key", c.apiKey)
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/apis/mlj_1/" + endpoint
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("maloja request %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("read maloja response: %w", err)
	}

	var env envelope
	_ = json.Unmarshal(body, &env)
	if env.Error != nil || resp.StatusCode >= 400 || env.Status == "error" || env.Status == "failure" {
		ae := &APIError{Status: resp.StatusCode}
		if env.Error != nil {
			ae.Type, ae.Desc, ae.Value = env.Error.Type, env.Error.Desc, env.Error.Value
		}
		return ae
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode maloja %s response: %w", endpoint, err)
	}
	return nil
}

// Album identifies an album.
type Album struct {
	Artists []string `json:"artists"`
	Title   string   `json:"albumtitle"`
}

// Track identifies a track.
type Track struct {
	Artists []string `json:"artists"`
	Title   string   `json:"title"`
	Album   *Album   `json:"album"`
	Length  *int     `json:"length"`
}

// Scrobble is one play.
type Scrobble struct {
	Time     int64  `json:"time"`
	Track    Track  `json:"track"`
	Duration *int   `json:"duration"`
	Origin   string `json:"origin"`
}

// TimeRange is the range block Maloja attaches to stepped results.
type TimeRange struct {
	From        string `json:"fromstring"`
	To          string `json:"tostr"`
	FromStamp   int64  `json:"fromstamp"`
	ToStamp     int64  `json:"tostamp"`
	Description string `json:"description"`
}

// ArtistChartEntry is a row of charts/artists.
type ArtistChartEntry struct {
	Rank       int      `json:"rank"`
	Artist     string   `json:"artist"`
	Scrobbles  int      `json:"scrobbles"`
	Associated []string `json:"associated_artists"`
}

// TrackChartEntry is a row of charts/tracks.
type TrackChartEntry struct {
	Rank      int   `json:"rank"`
	Track     Track `json:"track"`
	Scrobbles int   `json:"scrobbles"`
}

// AlbumChartEntry is a row of charts/albums.
type AlbumChartEntry struct {
	Rank      int   `json:"rank"`
	Album     Album `json:"album"`
	Scrobbles int   `json:"scrobbles"`
}

// PulseEntry is a row of pulse.
type PulseEntry struct {
	Range     TimeRange `json:"range"`
	Scrobbles int       `json:"scrobbles"`
}

// TopEntry is a row of top/artists, top/tracks or top/albums: the number one
// of one time step. Exactly one of Artist, Track, Album is set (or none if
// nothing was played in that step).
type TopEntry struct {
	Range     TimeRange `json:"range"`
	Artist    *string   `json:"artist"`
	Track     *Track    `json:"track"`
	Album     *Album    `json:"album"`
	Scrobbles int       `json:"scrobbles"`
}

// Years is a list of years. Maloja reports them as strings or numbers
// depending on version, so both are accepted.
type Years []int

// UnmarshalJSON accepts ["2019", 2020, ...].
func (y *Years) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(Years, 0, len(raw))
	for _, r := range raw {
		var n int
		if err := json.Unmarshal(r, &n); err == nil {
			out = append(out, n)
			continue
		}
		var s string
		if err := json.Unmarshal(r, &s); err != nil {
			return fmt.Errorf("medal year %s: %w", r, err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("medal year %q: %w", s, err)
		}
		out = append(out, n)
	}
	*y = out
	return nil
}

// Medals lists the years an entity finished first, second or third.
type Medals struct {
	Gold   Years `json:"gold"`
	Silver Years `json:"silver"`
	Bronze Years `json:"bronze"`
}

// ArtistInfo is the artistinfo response.
type ArtistInfo struct {
	Artist        string   `json:"artist"`
	Scrobbles     int      `json:"scrobbles"`
	Position      *int     `json:"position"`
	Associated    []string `json:"associated"`
	Medals        Medals   `json:"medals"`
	TopWeeks      int      `json:"topweeks"`
	Certification *string  `json:"certification"`
}

// TrackInfo is the trackinfo response.
type TrackInfo struct {
	Track         Track   `json:"track"`
	Scrobbles     int     `json:"scrobbles"`
	Position      *int    `json:"position"`
	Medals        Medals  `json:"medals"`
	TopWeeks      int     `json:"topweeks"`
	Certification *string `json:"certification"`
}

// AlbumInfo is the albuminfo response.
type AlbumInfo struct {
	Album         Album   `json:"album"`
	Scrobbles     int     `json:"scrobbles"`
	Position      *int    `json:"position"`
	Medals        Medals  `json:"medals"`
	TopWeeks      int     `json:"topweeks"`
	Certification *string `json:"certification"`
}

// SearchResult is the search response.
type SearchResult struct {
	Artists []struct {
		Artist string `json:"artist"`
	} `json:"artists"`
	Tracks []struct {
		Track Track `json:"track"`
	} `json:"tracks"`
	Albums []struct {
		Album Album `json:"album"`
	} `json:"albums"`
}

// ServerInfo is the serverinfo response.
type ServerInfo struct {
	Name          string `json:"name"`
	VersionString string `json:"versionstring"`
	DBStatus      struct {
		Healthy  bool `json:"healthy"`
		Complete bool `json:"complete"`
	} `json:"db_status"`
}

// Scrobbles returns plays matching q (filters, range, page, perpage, reverse).
func (c *Client) Scrobbles(ctx context.Context, q url.Values) ([]Scrobble, error) {
	var r struct {
		List []Scrobble `json:"list"`
	}
	err := c.get(ctx, "scrobbles", q, &r)
	return r.List, err
}

// NumScrobbles counts plays matching q.
func (c *Client) NumScrobbles(ctx context.Context, q url.Values) (int, error) {
	var r struct {
		Amount int `json:"amount"`
	}
	err := c.get(ctx, "numscrobbles", q, &r)
	return r.Amount, err
}

// ChartArtists returns the artist chart for q.
func (c *Client) ChartArtists(ctx context.Context, q url.Values) ([]ArtistChartEntry, error) {
	var r struct {
		List []ArtistChartEntry `json:"list"`
	}
	err := c.get(ctx, "charts/artists", q, &r)
	return r.List, err
}

// ChartTracks returns the track chart for q.
func (c *Client) ChartTracks(ctx context.Context, q url.Values) ([]TrackChartEntry, error) {
	var r struct {
		List []TrackChartEntry `json:"list"`
	}
	err := c.get(ctx, "charts/tracks", q, &r)
	return r.List, err
}

// ChartAlbums returns the album chart for q.
func (c *Client) ChartAlbums(ctx context.Context, q url.Values) ([]AlbumChartEntry, error) {
	var r struct {
		List []AlbumChartEntry `json:"list"`
	}
	err := c.get(ctx, "charts/albums", q, &r)
	return r.List, err
}

// Pulse returns play counts per time step.
func (c *Client) Pulse(ctx context.Context, q url.Values) ([]PulseEntry, error) {
	var r struct {
		List []PulseEntry `json:"list"`
	}
	err := c.get(ctx, "pulse", q, &r)
	return r.List, err
}

// Top returns the number one artist/track/album per time step. kind is
// "artists", "tracks" or "albums".
func (c *Client) Top(ctx context.Context, kind string, q url.Values) ([]TopEntry, error) {
	switch kind {
	case "artists", "tracks", "albums":
	default:
		return nil, fmt.Errorf("unknown top kind %q", kind)
	}
	var r struct {
		List []TopEntry `json:"list"`
	}
	err := c.get(ctx, "top/"+kind, q, &r)
	return r.List, err
}

// ArtistInfo returns stats for one artist.
func (c *Client) ArtistInfo(ctx context.Context, artist string) (*ArtistInfo, error) {
	var r ArtistInfo
	if err := c.get(ctx, "artistinfo", url.Values{"artist": {artist}}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// TrackInfo returns stats for one track.
func (c *Client) TrackInfo(ctx context.Context, artists []string, title string) (*TrackInfo, error) {
	q := url.Values{"title": {title}, "trackartist": artists}
	var r TrackInfo
	if err := c.get(ctx, "trackinfo", q, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// AlbumInfo returns stats for one album.
func (c *Client) AlbumInfo(ctx context.Context, artists []string, title string) (*AlbumInfo, error) {
	q := url.Values{"albumtitle": {title}}
	if len(artists) > 0 {
		q["albumartist"] = artists
	}
	var r AlbumInfo
	if err := c.get(ctx, "albuminfo", q, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Search finds artists, tracks and albums by name.
func (c *Client) Search(ctx context.Context, query string, max int) (*SearchResult, error) {
	var r SearchResult
	q := url.Values{"query": {query}, "max": {fmt.Sprint(max)}}
	if err := c.get(ctx, "search", q, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ServerInfo returns the Maloja version and database health.
func (c *Client) ServerInfo(ctx context.Context) (*ServerInfo, error) {
	var r ServerInfo
	if err := c.get(ctx, "serverinfo", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
