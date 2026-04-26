package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/db"
	"github.com/ianfoo/feedrig/internal/enrich"
	"github.com/ianfoo/feedrig/internal/groups"
	"github.com/ianfoo/feedrig/internal/ingest"
	"github.com/ianfoo/feedrig/internal/schedule"
	"github.com/ianfoo/feedrig/internal/settings"
	"github.com/ianfoo/feedrig/internal/summarize"
	"github.com/ianfoo/feedrig/internal/transcribe"
	"github.com/ianfoo/feedrig/internal/ttl"
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
	summarizer := flag.String("summarizer", "auto", "summarizer: auto|ollama|openrouter|stub|none (auto = ollama if reachable else stub)")
	ollamaURL := flag.String("ollama-url", "http://localhost:11434", "Ollama base URL")
	ollamaModel := flag.String("ollama-model", "llama3.2:3b", "Ollama model id (must be pulled locally first: `ollama pull <name>`)")
	openrouterModel := flag.String("openrouter-model", "anthropic/claude-3.5-haiku", "OpenRouter model id")
	whisperModel := flag.String("whisper-model", "", "path to whisper.cpp model (.bin); empty disables transcription")
	categoriesFlag := flag.String("categories", "news,political-commentary,music,bass-guitar,baking,pizza,food,comedy,tech", "comma-separated category menu shown to the summarizer")
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

	enrichStore := enrich.NewStore(conn)
	worker := &enrich.Worker{
		Videos:      videos,
		Enrich:      enrichStore,
		Transcriber: buildTranscriber(*whisperModel),
		Summarizer:  buildSummarizer(*summarizer, *ollamaURL, *ollamaModel, *openrouterModel, log),
		Categories:  splitCSV(*categoriesFlag),
		Log:         log,
	}
	ingestSvc.SetEnricher(worker)

	settingsStore := settings.NewStore(conn)
	groupsStore := groups.NewStore(conn)

	srv, err := web.NewServer(creators, videos, enrichStore, settingsStore, groupsStore, ingestSvc, mediaAbs, log)
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

	go worker.Run(ctx)

	scheduler := &schedule.Scheduler{
		Creators: creators,
		Settings: settingsStore,
		Ingest:   ingestSvc,
		Log:      log,
	}
	srv.SetScheduler(scheduler)
	go scheduler.Run(ctx)

	sweeper := &ttl.Sweeper{
		DB:       conn,
		Videos:   videos,
		Settings: settingsStore,
		Log:      log,
	}
	go sweeper.Run(ctx)

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

func buildSummarizer(name, ollamaURL, ollamaModel, openrouterModel string, log *slog.Logger) summarize.Summarizer {
	switch name {
	case "ollama":
		return summarize.Ollama{BaseURL: ollamaURL, Model: ollamaModel}
	case "openrouter":
		key := os.Getenv("OPENROUTER_API_KEY")
		return summarize.OpenRouter{APIKey: key, Model: openrouterModel}
	case "none":
		return summarize.Noop{}
	case "stub":
		return summarize.Stub{}
	case "auto", "":
		// Probe Ollama; if it answers, use it. Otherwise fall back to the
		// offline Stub so summaries are still produced (even if useless),
		// rather than silently doing nothing.
		client := &http.Client{Timeout: 1500 * time.Millisecond}
		req, _ := http.NewRequest("GET", strings.TrimRight(ollamaURL, "/")+"/api/tags", nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.Info("summarizer: ollama detected", "url", ollamaURL, "model", ollamaModel)
				return summarize.Ollama{BaseURL: ollamaURL, Model: ollamaModel}
			}
		}
		log.Info("summarizer: ollama not reachable; using stub. Run `ollama serve` and `ollama pull " + ollamaModel + "` to enable real summaries.")
		return summarize.Stub{}
	default:
		log.Warn("unknown summarizer; using stub", "value", name)
		return summarize.Stub{}
	}
}

func buildTranscriber(modelPath string) transcribe.Transcriber {
	if modelPath == "" {
		return transcribe.Noop{}
	}
	return transcribe.WhisperCpp{ModelPath: modelPath}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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
