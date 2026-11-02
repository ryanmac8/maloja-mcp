// Package tools registers the Maloja MCP tools on an MCP server.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanmac8/maloja-mcp/internal/maloja"
	"github.com/ryanmac8/maloja-mcp/internal/timerange"
)

const (
	defaultLimit = 25
	maxLimit     = 500
)

// Deps are what the tools need at runtime.
type Deps struct {
	Client   *maloja.Client
	Location *time.Location
	Now      func() time.Time // overridable for tests
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().In(d.Location)
	}
	return time.Now().In(d.Location)
}

// Register adds every tool to s.
func Register(s *mcp.Server, d *Deps) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(false)}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_recent_scrobbles",
		Title: "Recent scrobbles",
		Description: "List individual plays (scrobbles), newest first, optionally filtered by artist, track, album and time period. " +
			"Use this for 'what did I listen to yesterday' or 'when did I last play X'. Times are in the listener's local time zone.",
		Annotations: ro,
	}, d.recentScrobbles)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "count_scrobbles",
		Title:       "Count scrobbles",
		Description: "Count plays, optionally filtered by artist, track, album and time period. Use for 'how many times did I play X in 2024'.",
		Annotations: ro,
	}, d.countScrobbles)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_top_artists",
		Title:       "Top artists",
		Description: "Most played artists for a time period, ranked by play count.",
		Annotations: ro,
	}, d.topArtists)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_top_tracks",
		Title:       "Top tracks",
		Description: "Most played tracks for a time period, ranked by play count. Optionally limited to one artist or one album.",
		Annotations: ro,
	}, d.topTracks)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_top_albums",
		Title:       "Top albums",
		Description: "Most played albums for a time period, ranked by play count. Optionally limited to one artist.",
		Annotations: ro,
	}, d.topAlbums)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_listening_summary",
		Title: "Listening summary",
		Description: "One-call overview of a time period: total plays, top artists, top tracks and top albums. " +
			"Best first call for questions like 'what did I listen to last month'.",
		Annotations: ro,
	}, d.summary)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_listening_over_time",
		Title: "Listening over time",
		Description: "Play counts per day, week, month or year (a time series), optionally for one artist, track or album. " +
			"Use for trends like 'how much did I listen to X each year'.",
		Annotations: ro,
	}, d.overTime)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_number_ones",
		Title:       "Number ones per period",
		Description: "The #1 artist, track or album for each week, month or year in a time period, e.g. 'my top artist of every year'.",
		Annotations: ro,
	}, d.numberOnes)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_artist_info",
		Title: "Artist stats",
		Description: "All-time stats for one artist: total plays, all-time rank, first and last play, weeks at #1, " +
			"yearly medals and their most played tracks. Use search_library first if unsure of the exact name.",
		Annotations: ro,
	}, d.artistInfo)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_track_info",
		Title: "Track stats",
		Description: "All-time stats for one track: total plays, all-time rank, first and last play, weeks at #1 and medals. " +
			"Needs the exact artist and title; use search_library first if unsure.",
		Annotations: ro,
	}, d.trackInfo)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_album_info",
		Title: "Album stats",
		Description: "All-time stats for one album: total plays, all-time rank, first and last play, and its most played tracks. " +
			"Use search_library first if unsure of the exact title.",
		Annotations: ro,
	}, d.albumInfo)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "search_library",
		Title: "Search library",
		Description: "Find artists, tracks and albums in the listening history by partial name. " +
			"Returns exact names to use with the other tools.",
		Annotations: ro,
	}, d.search)
}

func ptr[T any](v T) *T { return &v }

// ---- shared input handling ----

func filterQuery(artist, track, album string) (url.Values, error) {
	q := url.Values{}
	artist, track, album = strings.TrimSpace(artist), strings.TrimSpace(track), strings.TrimSpace(album)
	switch {
	case track != "" && album != "":
		return nil, errors.New("give either track or album, not both")
	case track != "":
		if artist == "" {
			return nil, errors.New("track needs artist as well (the exact artist name, see search_library)")
		}
		q.Set("title", track)
		q.Add("trackartist", artist)
	case album != "":
		q.Set("albumtitle", album)
		if artist != "" {
			q.Add("albumartist", artist)
		}
	case artist != "":
		q.Set("artist", artist)
	}
	return q, nil
}

func (d *Deps) applyRange(q url.Values, period, from, to string) (timerange.Range, error) {
	r, err := timerange.Resolve(period, from, to, d.now())
	if err != nil {
		return r, err
	}
	r.Apply(q)
	return r, nil
}

func clampLimit(n, def int) int {
	if n <= 0 {
		return def
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

// toolError turns err into a tool-level error the model can read and act on.
func toolError(err error) (*mcp.CallToolResult, any, error) {
	msg := err.Error()
	if maloja.IsNotFound(err) {
		msg += ". Check the exact spelling with search_library."
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
}

// ---- output shapes ----

type trackOut struct {
	Artists []string `json:"artists"`
	Title   string   `json:"title"`
	Album   string   `json:"album,omitempty"`
}

type albumOut struct {
	Artists []string `json:"artists,omitempty"`
	Title   string   `json:"title"`
}

func toTrack(t maloja.Track) trackOut {
	o := trackOut{Artists: t.Artists, Title: t.Title}
	if t.Album != nil {
		o.Album = t.Album.Title
	}
	return o
}

func toAlbum(a maloja.Album) albumOut { return albumOut{Artists: a.Artists, Title: a.Title} }

type scrobbleOut struct {
	Time string `json:"time"`
	trackOut
	Seconds *int   `json:"seconds_played,omitempty"`
	Source  string `json:"source,omitempty"`
}

func (d *Deps) fmtTime(unix int64) string {
	return time.Unix(unix, 0).In(d.Location).Format("2006-01-02 15:04 MST")
}

func (d *Deps) toScrobble(s maloja.Scrobble) scrobbleOut {
	return scrobbleOut{Time: d.fmtTime(s.Time), trackOut: toTrack(s.Track), Seconds: s.Duration, Source: s.Origin}
}

// ---- tools ----

type recentIn struct {
	Artist      string `json:"artist,omitempty" jsonschema:"only plays by this artist (exact name); required together with track"`
	Track       string `json:"track,omitempty" jsonschema:"only plays of this track title (exact); needs artist"`
	Album       string `json:"album,omitempty" jsonschema:"only plays from this album title (exact); artist optional"`
	Period      string `json:"period,omitempty" jsonschema:"today, yesterday, this_week, last_week, this_month, last_month, this_year, last_year, last_N_days, all_time, 2024, 2024-08, 2024-08-15, 2024-W42 or a month name; omit for all time"`
	From        string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD (ignored when period is set)"`
	To          string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD inclusive (ignored when period is set)"`
	Limit       int    `json:"limit,omitempty" jsonschema:"max plays to return (default 25, max 500)"`
	Page        int    `json:"page,omitempty" jsonschema:"0-based page for paging through older results"`
	OldestFirst bool   `json:"oldest_first,omitempty" jsonschema:"return the oldest plays first (e.g. to find the first time something was played)"`
}

func (d *Deps) recentScrobbles(ctx context.Context, _ *mcp.CallToolRequest, in recentIn) (*mcp.CallToolResult, any, error) {
	q, err := filterQuery(in.Artist, in.Track, in.Album)
	if err != nil {
		return toolError(err)
	}
	r, err := d.applyRange(q, in.Period, in.From, in.To)
	if err != nil {
		return toolError(err)
	}
	limit := clampLimit(in.Limit, defaultLimit)
	q.Set("perpage", fmt.Sprint(limit))
	if in.Page > 0 {
		q.Set("page", fmt.Sprint(in.Page))
	}
	if in.OldestFirst {
		q.Set("reverse", "no")
	}
	list, err := d.Client.Scrobbles(ctx, q)
	if err != nil {
		return toolError(err)
	}
	out := make([]scrobbleOut, 0, len(list))
	for _, s := range list {
		out = append(out, d.toScrobble(s))
	}
	return jsonResult(map[string]any{
		"period":    r.String(),
		"timezone":  d.Location.String(),
		"page":      in.Page,
		"has_more":  len(list) == limit,
		"scrobbles": out,
	})
}

type countIn struct {
	Artist string `json:"artist,omitempty" jsonschema:"only plays by this artist (exact name); required together with track"`
	Track  string `json:"track,omitempty" jsonschema:"only plays of this track title (exact); needs artist"`
	Album  string `json:"album,omitempty" jsonschema:"only plays from this album title (exact)"`
	Period string `json:"period,omitempty" jsonschema:"today, yesterday, this_week, last_week, this_month, last_month, this_year, last_year, last_N_days, all_time, 2024, 2024-08, 2024-08-15, 2024-W42 or a month name; omit for all time"`
	From   string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD (ignored when period is set)"`
	To     string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD inclusive (ignored when period is set)"`
}

func (d *Deps) countScrobbles(ctx context.Context, _ *mcp.CallToolRequest, in countIn) (*mcp.CallToolResult, any, error) {
	q, err := filterQuery(in.Artist, in.Track, in.Album)
	if err != nil {
		return toolError(err)
	}
	r, err := d.applyRange(q, in.Period, in.From, in.To)
	if err != nil {
		return toolError(err)
	}
	n, err := d.Client.NumScrobbles(ctx, q)
	if err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{"period": r.String(), "scrobbles": n})
}

type chartIn struct {
	Period string `json:"period,omitempty" jsonschema:"today, yesterday, this_week, last_week, this_month, last_month, this_year, last_year, last_N_days, all_time, 2024, 2024-08, 2024-08-15, 2024-W42 or a month name; omit for all time"`
	From   string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD (ignored when period is set)"`
	To     string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD inclusive (ignored when period is set)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many entries to return (default 25, max 500)"`
}

type artistRow struct {
	Rank      int    `json:"rank"`
	Artist    string `json:"artist"`
	Scrobbles int    `json:"scrobbles"`
}

func (d *Deps) chartArtists(ctx context.Context, q url.Values, limit int) ([]artistRow, int, error) {
	list, err := d.Client.ChartArtists(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	total := len(list)
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]artistRow, 0, len(list))
	for _, e := range list {
		out = append(out, artistRow{Rank: e.Rank, Artist: e.Artist, Scrobbles: e.Scrobbles})
	}
	return out, total, nil
}

func (d *Deps) topArtists(ctx context.Context, _ *mcp.CallToolRequest, in chartIn) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	r, err := d.applyRange(q, in.Period, in.From, in.To)
	if err != nil {
		return toolError(err)
	}
	rows, total, err := d.chartArtists(ctx, q, clampLimit(in.Limit, defaultLimit))
	if err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{"period": r.String(), "distinct_artists": total, "artists": rows})
}

type trackChartIn struct {
	Artist string `json:"artist,omitempty" jsonschema:"only tracks by this artist (exact name)"`
	Album  string `json:"album,omitempty" jsonschema:"only tracks on this album (exact title)"`
	Period string `json:"period,omitempty" jsonschema:"today, yesterday, this_week, last_week, this_month, last_month, this_year, last_year, last_N_days, all_time, 2024, 2024-08, 2024-08-15, 2024-W42 or a month name; omit for all time"`
	From   string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD (ignored when period is set)"`
	To     string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD inclusive (ignored when period is set)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many entries to return (default 25, max 500)"`
}

type trackRow struct {
	Rank int `json:"rank"`
	trackOut
	Scrobbles int `json:"scrobbles"`
}

func (d *Deps) chartTracks(ctx context.Context, q url.Values, limit int) ([]trackRow, int, error) {
	list, err := d.Client.ChartTracks(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	total := len(list)
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]trackRow, 0, len(list))
	for _, e := range list {
		out = append(out, trackRow{Rank: e.Rank, trackOut: toTrack(e.Track), Scrobbles: e.Scrobbles})
	}
	return out, total, nil
}

func (d *Deps) topTracks(ctx context.Context, _ *mcp.CallToolRequest, in trackChartIn) (*mcp.CallToolResult, any, error) {
	q, err := filterQuery(in.Artist, "", in.Album)
	if err != nil {
		return toolError(err)
	}
	r, err := d.applyRange(q, in.Period, in.From, in.To)
	if err != nil {
		return toolError(err)
	}
	rows, total, err := d.chartTracks(ctx, q, clampLimit(in.Limit, defaultLimit))
	if err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{"period": r.String(), "distinct_tracks": total, "tracks": rows})
}

type albumChartIn struct {
	Artist string `json:"artist,omitempty" jsonschema:"only albums by this artist (exact name)"`
	Period string `json:"period,omitempty" jsonschema:"today, yesterday, this_week, last_week, this_month, last_month, this_year, last_year, last_N_days, all_time, 2024, 2024-08, 2024-08-15, 2024-W42 or a month name; omit for all time"`
	From   string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD (ignored when period is set)"`
	To     string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD inclusive (ignored when period is set)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many entries to return (default 25, max 500)"`
}

type albumRow struct {
	Rank int `json:"rank"`
	albumOut
	Scrobbles int `json:"scrobbles"`
}

func (d *Deps) chartAlbums(ctx context.Context, q url.Values, limit int) ([]albumRow, int, error) {
	list, err := d.Client.ChartAlbums(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	total := len(list)
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]albumRow, 0, len(list))
	for _, e := range list {
		out = append(out, albumRow{Rank: e.Rank, albumOut: toAlbum(e.Album), Scrobbles: e.Scrobbles})
	}
	return out, total, nil
}

func (d *Deps) topAlbums(ctx context.Context, _ *mcp.CallToolRequest, in albumChartIn) (*mcp.CallToolResult, any, error) {
	q, err := filterQuery(in.Artist, "", "")
	if err != nil {
		return toolError(err)
	}
	r, err := d.applyRange(q, in.Period, in.From, in.To)
	if err != nil {
		return toolError(err)
	}
	rows, total, err := d.chartAlbums(ctx, q, clampLimit(in.Limit, defaultLimit))
	if err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{"period": r.String(), "distinct_albums": total, "albums": rows})
}

type summaryIn struct {
	Period string `json:"period,omitempty" jsonschema:"today, yesterday, this_week, last_week, this_month, last_month, this_year, last_year, last_N_days, all_time, 2024, 2024-08, 2024-08-15, 2024-W42 or a month name; omit for all time"`
	From   string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD (ignored when period is set)"`
	To     string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD inclusive (ignored when period is set)"`
	Top    int    `json:"top,omitempty" jsonschema:"entries per list (default 10, max 50)"`
}

func (d *Deps) summary(ctx context.Context, _ *mcp.CallToolRequest, in summaryIn) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	r, err := d.applyRange(q, in.Period, in.From, in.To)
	if err != nil {
		return toolError(err)
	}
	top := in.Top
	if top <= 0 {
		top = 10
	}
	if top > 50 {
		top = 50
	}
	total, err := d.Client.NumScrobbles(ctx, q)
	if err != nil {
		return toolError(err)
	}
	artists, nArtists, err := d.chartArtists(ctx, q, top)
	if err != nil {
		return toolError(err)
	}
	tracks, nTracks, err := d.chartTracks(ctx, q, top)
	if err != nil {
		return toolError(err)
	}
	albums, nAlbums, err := d.chartAlbums(ctx, q, top)
	if err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{
		"period":           r.String(),
		"total_scrobbles":  total,
		"distinct_artists": nArtists,
		"distinct_tracks":  nTracks,
		"distinct_albums":  nAlbums,
		"top_artists":      artists,
		"top_tracks":       tracks,
		"top_albums":       albums,
	})
}

type overTimeIn struct {
	Step       string `json:"step,omitempty" jsonschema:"bucket size: day, week, month (default) or year"`
	Artist     string `json:"artist,omitempty" jsonschema:"only plays by this artist (exact name); required together with track"`
	Track      string `json:"track,omitempty" jsonschema:"only plays of this track title (exact); needs artist"`
	Album      string `json:"album,omitempty" jsonschema:"only plays from this album title (exact)"`
	Period     string `json:"period,omitempty" jsonschema:"range to cover: this_year, last_year, last_N_days, all_time, 2024, 2024-08 etc. Default depends on step: day=last_30_days, week=last_90_days, month=last_365_days, year=all_time"`
	From       string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD (ignored when period is set)"`
	To         string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD inclusive (ignored when period is set)"`
	Cumulative bool   `json:"cumulative,omitempty" jsonschema:"running total instead of per-bucket counts"`
}

var defaultStepPeriod = map[string]string{
	"day":   "last_30_days",
	"week":  "last_90_days",
	"month": "last_365_days",
	"year":  "all_time",
}

func (d *Deps) overTime(ctx context.Context, _ *mcp.CallToolRequest, in overTimeIn) (*mcp.CallToolResult, any, error) {
	step := strings.ToLower(strings.TrimSpace(in.Step))
	if step == "" {
		step = "month"
	}
	if _, ok := defaultStepPeriod[step]; !ok {
		return toolError(fmt.Errorf("step must be day, week, month or year, got %q", in.Step))
	}
	q, err := filterQuery(in.Artist, in.Track, in.Album)
	if err != nil {
		return toolError(err)
	}
	period := in.Period
	if period == "" && in.From == "" && in.To == "" {
		period = defaultStepPeriod[step]
	}
	r, err := d.applyRange(q, period, in.From, in.To)
	if err != nil {
		return toolError(err)
	}
	q.Set("step", step)
	if in.Cumulative {
		q.Set("cumulative", "yes")
	}
	list, err := d.Client.Pulse(ctx, q)
	if err != nil {
		return toolError(err)
	}
	const maxRows = 400
	truncated := false
	if len(list) > maxRows {
		list = list[len(list)-maxRows:]
		truncated = true
	}
	type row struct {
		Period    string `json:"period"`
		Scrobbles int    `json:"scrobbles"`
	}
	out := make([]row, 0, len(list))
	for _, e := range list {
		out = append(out, row{Period: e.Range.Description, Scrobbles: e.Scrobbles})
	}
	res := map[string]any{"period": r.String(), "step": step, "series": out}
	if truncated {
		res["note"] = fmt.Sprintf("only the most recent %d buckets are shown; narrow the period or use a bigger step", maxRows)
	}
	return jsonResult(res)
}

type numberOnesIn struct {
	Kind   string `json:"kind,omitempty" jsonschema:"artist (default), track or album"`
	Step   string `json:"step,omitempty" jsonschema:"week, month or year (default)"`
	Period string `json:"period,omitempty" jsonschema:"range to cover, e.g. all_time (default for year), last_365_days (default for month), last_90_days (default for week), 2024"`
	From   string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD (ignored when period is set)"`
	To     string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD inclusive (ignored when period is set)"`
}

func (d *Deps) numberOnes(ctx context.Context, _ *mcp.CallToolRequest, in numberOnesIn) (*mcp.CallToolResult, any, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	switch kind {
	case "", "artist", "artists":
		kind = "artists"
	case "track", "tracks", "song", "songs":
		kind = "tracks"
	case "album", "albums":
		kind = "albums"
	default:
		return toolError(fmt.Errorf("kind must be artist, track or album, got %q", in.Kind))
	}
	step := strings.ToLower(strings.TrimSpace(in.Step))
	if step == "" {
		step = "year"
	}
	if step != "week" && step != "month" && step != "year" {
		return toolError(fmt.Errorf("step must be week, month or year, got %q", in.Step))
	}
	period := in.Period
	if period == "" && in.From == "" && in.To == "" {
		period = defaultStepPeriod[step]
	}
	q := url.Values{}
	r, err := d.applyRange(q, period, in.From, in.To)
	if err != nil {
		return toolError(err)
	}
	q.Set("step", step)
	list, err := d.Client.Top(ctx, kind, q)
	if err != nil {
		return toolError(err)
	}
	type row struct {
		Period    string    `json:"period"`
		Artist    string    `json:"artist,omitempty"`
		Track     *trackOut `json:"track,omitempty"`
		Album     *albumOut `json:"album,omitempty"`
		Scrobbles int       `json:"scrobbles"`
	}
	out := make([]row, 0, len(list))
	for _, e := range list {
		rw := row{Period: e.Range.Description, Scrobbles: e.Scrobbles}
		switch {
		case e.Artist != nil:
			rw.Artist = *e.Artist
		case e.Track != nil:
			t := toTrack(*e.Track)
			rw.Track = &t
		case e.Album != nil:
			a := toAlbum(*e.Album)
			rw.Album = &a
		default:
			continue // nothing played in this step
		}
		out = append(out, rw)
	}
	return jsonResult(map[string]any{"period": r.String(), "step": step, "kind": kind, "number_ones": out})
}

// firstLast finds the oldest and newest play matching q.
func (d *Deps) firstLast(ctx context.Context, q url.Values) (first, last string, err error) {
	newest := cloneValues(q)
	newest.Set("perpage", "1")
	l, err := d.Client.Scrobbles(ctx, newest)
	if err != nil || len(l) == 0 {
		return "", "", err
	}
	last = d.fmtTime(l[0].Time)
	oldest := cloneValues(q)
	oldest.Set("perpage", "1")
	oldest.Set("reverse", "no")
	l, err = d.Client.Scrobbles(ctx, oldest)
	if err != nil || len(l) == 0 {
		return "", last, err
	}
	return d.fmtTime(l[0].Time), last, nil
}

func cloneValues(v url.Values) url.Values {
	c := url.Values{}
	for k, vs := range v {
		c[k] = append([]string(nil), vs...)
	}
	return c
}

type medalsOut struct {
	Gold   maloja.Years `json:"gold,omitempty"`
	Silver maloja.Years `json:"silver,omitempty"`
	Bronze maloja.Years `json:"bronze,omitempty"`
}

func toMedals(m maloja.Medals) *medalsOut {
	if len(m.Gold)+len(m.Silver)+len(m.Bronze) == 0 {
		return nil
	}
	return &medalsOut{Gold: m.Gold, Silver: m.Silver, Bronze: m.Bronze}
}

type artistInfoIn struct {
	Artist    string `json:"artist" jsonschema:"exact artist name"`
	TopTracks int    `json:"top_tracks,omitempty" jsonschema:"how many of their most played tracks to include (default 10, max 50)"`
}

func (d *Deps) artistInfo(ctx context.Context, _ *mcp.CallToolRequest, in artistInfoIn) (*mcp.CallToolResult, any, error) {
	name := strings.TrimSpace(in.Artist)
	if name == "" {
		return toolError(errors.New("artist is required"))
	}
	info, err := d.Client.ArtistInfo(ctx, name)
	if err != nil {
		return toolError(err)
	}
	q := url.Values{"artist": {info.Artist}}
	first, last, err := d.firstLast(ctx, q)
	if err != nil {
		return toolError(err)
	}
	n := in.TopTracks
	if n <= 0 {
		n = 10
	}
	if n > 50 {
		n = 50
	}
	tracks, nTracks, err := d.chartTracks(ctx, q, n)
	if err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{
		"artist":             info.Artist,
		"scrobbles":          info.Scrobbles,
		"all_time_rank":      info.Position,
		"first_played":       first,
		"last_played":        last,
		"weeks_at_number_1":  info.TopWeeks,
		"yearly_medals":      toMedals(info.Medals),
		"associated_artists": info.Associated,
		"distinct_tracks":    nTracks,
		"top_tracks":         tracks,
	})
}

type trackInfoIn struct {
	Artist string `json:"artist" jsonschema:"exact artist name (for multi-artist tracks, any one of the credited artists)"`
	Track  string `json:"track" jsonschema:"exact track title"`
}

func (d *Deps) trackInfo(ctx context.Context, _ *mcp.CallToolRequest, in trackInfoIn) (*mcp.CallToolResult, any, error) {
	artist, title := strings.TrimSpace(in.Artist), strings.TrimSpace(in.Track)
	if artist == "" || title == "" {
		return toolError(errors.New("artist and track are both required"))
	}
	info, err := d.Client.TrackInfo(ctx, []string{artist}, title)
	if err != nil {
		return toolError(err)
	}
	q := url.Values{"title": {info.Track.Title}, "trackartist": info.Track.Artists}
	first, last, err := d.firstLast(ctx, q)
	if err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{
		"track":             toTrack(info.Track),
		"scrobbles":         info.Scrobbles,
		"all_time_rank":     info.Position,
		"first_played":      first,
		"last_played":       last,
		"weeks_at_number_1": info.TopWeeks,
		"yearly_medals":     toMedals(info.Medals),
	})
}

type albumInfoIn struct {
	Album  string `json:"album" jsonschema:"exact album title"`
	Artist string `json:"artist,omitempty" jsonschema:"album artist (recommended; omit for compilations)"`
}

func (d *Deps) albumInfo(ctx context.Context, _ *mcp.CallToolRequest, in albumInfoIn) (*mcp.CallToolResult, any, error) {
	title := strings.TrimSpace(in.Album)
	if title == "" {
		return toolError(errors.New("album is required"))
	}
	var artists []string
	if a := strings.TrimSpace(in.Artist); a != "" {
		artists = []string{a}
	}
	info, err := d.Client.AlbumInfo(ctx, artists, title)
	if err != nil {
		return toolError(err)
	}
	q := url.Values{"albumtitle": {info.Album.Title}}
	if len(info.Album.Artists) > 0 {
		q["albumartist"] = info.Album.Artists
	}
	first, last, err := d.firstLast(ctx, q)
	if err != nil {
		return toolError(err)
	}
	tracks, _, err := d.chartTracks(ctx, q, 50)
	if err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{
		"album":             toAlbum(info.Album),
		"scrobbles":         info.Scrobbles,
		"all_time_rank":     info.Position,
		"first_played":      first,
		"last_played":       last,
		"weeks_at_number_1": info.TopWeeks,
		"yearly_medals":     toMedals(info.Medals),
		"tracks":            tracks,
	})
}

type searchIn struct {
	Query string `json:"query" jsonschema:"part of an artist, track or album name"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results per category (default 10, max 50)"`
}

func (d *Deps) search(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return toolError(errors.New("query is required"))
	}
	n := in.Limit
	if n <= 0 {
		n = 10
	}
	if n > 50 {
		n = 50
	}
	res, err := d.Client.Search(ctx, query, n)
	if err != nil {
		return toolError(err)
	}
	artists := make([]string, 0, len(res.Artists))
	for _, a := range res.Artists {
		artists = append(artists, a.Artist)
	}
	tracks := make([]trackOut, 0, len(res.Tracks))
	for _, t := range res.Tracks {
		tracks = append(tracks, toTrack(t.Track))
	}
	albums := make([]albumOut, 0, len(res.Albums))
	for _, a := range res.Albums {
		albums = append(albums, toAlbum(a.Album))
	}
	return jsonResult(map[string]any{"artists": artists, "tracks": tracks, "albums": albums})
}
