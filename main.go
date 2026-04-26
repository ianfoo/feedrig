package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/db"
	"github.com/ianfoo/feedrig/internal/ingest"
	"github.com/ianfoo/feedrig/internal/video"
	"github.com/ianfoo/feedrig/internal/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7777", "listen address")
	dataDir := flag.String("data", "data", "directory for sqlite db")
	mediaDir := flag.String("media", "media", "directory for downloaded videos")
	cookies := flag.String("cookies", "", "optional path to instagram cookies file (yt-dlp format)")
	discoverer := flag.String("discoverer", "auto", "discovery strategy: auto|chromedp|instago|none")
	chromePath := flag.String("chrome", "", "path to chromium/chrome binary (default: search PATH)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Error("mkdir data", "err", err); os.Exit(1)
	}
	if err := os.MkdirAll(*mediaDir, 0o755); err != nil {
		log.Error("mkdir media", "err", err); os.Exit(1)
	}
	mediaAbs, err := filepath.Abs(*mediaDir)
	if err != nil {
		log.Error("abs media", "err", err); os.Exit(1)
	}

	conn, err := db.Open(filepath.Join(*dataDir, "feedrig.db"))
	if err != nil {
		log.Error("open db", "err", err); os.Exit(1)
	}
	defer conn.Close()

	creators := creator.NewStore(conn)
	videos := video.NewStore(conn)

	disc := buildDiscoverer(*discoverer, *chromePath, log)
	ingestSvc := ingest.NewService(
		disc,
		ingest.YtDlpDownloader{CookieFile: *cookies},
		creators, videos, mediaAbs, log,
	)

	srv, err := web.NewServer(creators, videos, ingestSvc, mediaAbs, log)
	if err != nil {
		log.Error("server init", "err", err); os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", *addr, "data", *dataDir, "media", mediaAbs)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("listen", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutCtx)
}

func buildDiscoverer(name, chromePath string, log *slog.Logger) ingest.Discoverer {
	switch name {
	case "chromedp":
		return ingest.ChromedpDiscoverer{ChromePath: chromePath, MaxScrolls: 1}
	case "instago":
		return ingest.InstagoDiscoverer{}
	case "none":
		return ingest.NoopDiscoverer{}
	case "auto", "":
		return ingest.ChainDiscoverer{
			Steps: []ingest.NamedDiscoverer{
				{Name: "chromedp", Disc: ingest.ChromedpDiscoverer{ChromePath: chromePath, MaxScrolls: 1}},
				{Name: "instago", Disc: ingest.InstagoDiscoverer{}},
			},
			Log: log,
		}
	default:
		log.Warn("unknown discoverer; using auto", "value", name)
		return ingest.ChainDiscoverer{
			Steps: []ingest.NamedDiscoverer{
				{Name: "chromedp", Disc: ingest.ChromedpDiscoverer{ChromePath: chromePath, MaxScrolls: 1}},
				{Name: "instago", Disc: ingest.InstagoDiscoverer{}},
			},
			Log: log,
		}
	}
}
