# maloja-mcp

A read-only [Model Context Protocol](https://modelcontextprotocol.io) server for
[Maloja](https://github.com/krateng/maloja), the self-hosted scrobble database.
It lets Claude (or any MCP client) answer questions about your listening history:

- "What did I listen to most last month?"
- "When did I first play ODESZA, and how many times have I played them in total?"
- "Who was my top artist every year since 2015?"
- "How much did I listen to music each month this year?"

It's written in Go and ships as one static binary in a distroless image (under 20 MB). It
uses Streamable HTTP on `/mcp` and runs stateless, so nothing breaks when it restarts.

## Tools

All tools are read-only and annotated as such.

| Tool | What it does |
| --- | --- |
| `get_listening_summary` | Total plays plus top artists, tracks and albums for a period. A good first call. |
| `get_recent_scrobbles` | Individual plays, newest first (or oldest first), filtered by artist/track/album/period. |
| `count_scrobbles` | Number of plays matching filters and a period. |
| `get_top_artists` | Artist chart for a period. |
| `get_top_tracks` | Track chart for a period, optionally for one artist or album. |
| `get_top_albums` | Album chart for a period, optionally for one artist. |
| `get_listening_over_time` | Plays per day/week/month/year, optionally for one artist/track/album. |
| `get_number_ones` | The #1 artist/track/album of each week/month/year. |
| `get_artist_info` | All-time plays, rank, first/last play, weeks at #1, medals, top tracks. |
| `get_track_info` | All-time plays, rank, first/last play, weeks at #1, medals. |
| `get_album_info` | All-time plays, rank, first/last play, per-track breakdown. |
| `search_library` | Find exact artist/track/album names by partial match. |

### Periods

Tools that take a `period` accept:

- relative: `today`, `yesterday`, `this_week`, `last_week`, `this_month`, `last_month`,
  `this_year`, `last_year`, `last_N_days` (e.g. `last_30_days`), `all_time`
- absolute: `2024`, `2024-08`, `2024-08-15`, `2024-W42`, or a month name (`august` means
  the most recent August)

Or pass explicit `from` / `to` dates (`YYYY-MM-DD`, inclusive). Relative periods are
worked out in `MALOJA_TIMEZONE`, with weeks running Monday to Sunday. Maloja only
understands some relative keywords natively (for example, it has no `lastmonth`), so
the server converts them into explicit dates before querying.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `MALOJA_URL` | required | Base URL of Maloja, e.g. `http://maloja:42010` |
| `MALOJA_TIMEZONE` | `$TZ`, else `UTC` | IANA zone for relative periods and displayed times. Match Maloja's `MALOJA_LOCATION_TIMEZONE`. |
| `MCP_AUTH_TOKEN` | unset | If set, `/mcp` requires `Authorization: Bearer <token>`. |
| `MCP_AUTH_TOKEN_FILE` | unset | Read the token from a file instead (Docker secrets). |
| `MALOJA_API_KEY` | unset | Sent as `key=`. A default Maloja doesn't need it for the endpoints used here. |
| `LISTEN_ADDR` | `:8080` | Listen address. |

`GET /healthz` checks that Maloja is reachable. It isn't authenticated, and the
image's Docker `HEALTHCHECK` uses it.

## Running

```sh
docker run -d --name maloja-mcp -p 3016:8080 \
  -e MALOJA_URL=http://maloja:42010 \
  -e MALOJA_TIMEZONE=America/New_York \
  -e MCP_AUTH_TOKEN="$(openssl rand -hex 32)" \
  ghcr.io/ryanmac8/maloja-mcp:latest
```

For Unraid there's a template at [`templates/maloja-mcp-unraid.xml`](templates/maloja-mcp-unraid.xml).

Point your MCP client or gateway at `http://<host>:3016/mcp` and send the bearer token.
Don't expose the server to the internet without auth: your listening history is personal
data.

## Development

```sh
go test -race ./...
go vet ./...
golangci-lint run
MALOJA_URL=http://localhost:42010 go run .
```

CI runs gofmt, vet, golangci-lint, the tests with the race detector, govulncheck and a
Docker build with a smoke test. Each push to `main` publishes a multi-arch
(amd64/arm64) image to `ghcr.io/ryanmac8/maloja-mcp:latest`, and `v*` tags publish
semver tags. Dependabot keeps Go modules, Actions and the base images current. Minor and
patch updates merge automatically once CI passes.

## License

MIT
