// Command maloja-mcp is a read-only Model Context Protocol server for a
// Maloja scrobble database, served over Streamable HTTP.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed zone data so MALOJA_TIMEZONE works in minimal images

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanmac8/maloja-mcp/internal/maloja"
	"github.com/ryanmac8/maloja-mcp/internal/tools"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

type config struct {
	MalojaURL    string
	MalojaAPIKey string
	ListenAddr   string
	AuthToken    string
	TimeZone     string
}

func loadConfig() (config, error) {
	c := config{
		MalojaURL:    os.Getenv("MALOJA_URL"),
		MalojaAPIKey: os.Getenv("MALOJA_API_KEY"),
		ListenAddr:   envOr("LISTEN_ADDR", ":8080"),
		AuthToken:    os.Getenv("MCP_AUTH_TOKEN"),
		TimeZone:     envOr("MALOJA_TIMEZONE", envOr("TZ", "UTC")),
	}
	if f := os.Getenv("MCP_AUTH_TOKEN_FILE"); f != "" && c.AuthToken == "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return c, fmt.Errorf("read MCP_AUTH_TOKEN_FILE: %w", err)
		}
		c.AuthToken = strings.TrimSpace(string(b))
	}
	if c.MalojaURL == "" {
		return c, errors.New("MALOJA_URL is required (e.g. http://maloja:42010)")
	}
	return c, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit (for Docker HEALTHCHECK)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := loadConfig()
	if *healthcheck {
		os.Exit(runHealthcheck(cfg.ListenAddr))
	}
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}

	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		logger.Error("invalid time zone", "tz", cfg.TimeZone, "err", err)
		os.Exit(1)
	}
	client, err := maloja.New(cfg.MalojaURL, cfg.MalojaAPIKey, nil)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           newHandler(cfg, client, loc),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("maloja-mcp listening", "addr", cfg.ListenAddr, "maloja", cfg.MalojaURL, "tz", loc.String(),
		"auth", cfg.AuthToken != "", "version", version)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server", "err", err)
		os.Exit(1)
	}
}

func newMCPServer(client *maloja.Client, loc *time.Location) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "maloja-mcp", Title: "Maloja listening history", Version: version}, &mcp.ServerOptions{
		Instructions: "Read-only access to the user's music listening history (scrobbles) stored in Maloja. " +
			"Start with get_listening_summary for broad questions about a time period, use search_library to find exact " +
			"artist/track/album names before calling the *_info tools, and get_recent_scrobbles for individual plays. " +
			"All times are in " + loc.String() + ".",
	})
	tools.Register(s, &tools.Deps{Client: client, Location: loc})
	return s
}

func newHandler(cfg config, client *maloja.Client, loc *time.Location) http.Handler {
	server := newMCPServer(client, loc)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", requireToken(cfg.AuthToken, mcpHandler))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		info, err := client.ServerInfo(ctx)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "maloja": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": version, "maloja_version": info.VersionString, "maloja_db_healthy": info.DBStatus.Healthy})
	})
	return mux
}

// requireToken enforces "Authorization: Bearer <token>" when token is set.
func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="maloja-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func runHealthcheck(addr string) int {
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	c := &http.Client{Timeout: 8 * time.Second}
	resp, err := c.Get("http://" + host + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthz:", resp.Status)
		return 1
	}
	return 0
}
