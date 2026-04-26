package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ianfoo/feedrig/internal/creator"
	"github.com/ianfoo/feedrig/internal/db"
	"github.com/ianfoo/feedrig/internal/digest"
	"github.com/ianfoo/feedrig/internal/enrich"
	"github.com/ianfoo/feedrig/internal/groups"
	"github.com/ianfoo/feedrig/internal/ingest"
	"github.com/ianfoo/feedrig/internal/notify"
	"github.com/ianfoo/feedrig/internal/mcp"
	"github.com/ianfoo/feedrig/internal/schedule"
	"github.com/ianfoo/feedrig/internal/settings"
	"github.com/ianfoo/feedrig/internal/summarize"
	"github.com/ianfoo/feedrig/internal/transcribe"
	"github.com/ianfoo/feedrig/internal/ttl"
	"github.com/ianfoo/feedrig/internal/video"
	"github.com/ianfoo/feedrig/internal/web"
)

func main() {
	// Subcommand dispatch: `feedrig <cmd> ...` runs that mode and exits.
	// The default (no subcommand) is the long-running HTTP+scheduler mode.
	if len(os.Args) >= 2 {
		cmd := os.Args[1]
		switch cmd {
		case "mcp", "sweep", "poll", "enrich", "digest":
			os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
			switch cmd {
			case "mcp":
				runMCP()
			case "sweep":
				runSweep()
			case "poll":
				runPoll()
			case "enrich":
				runEnrich()
			case "digest":
				runDigest()
			}
			return
		}
	}

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

// runSweep performs one TTL sweep pass and exits. Suited for cron / serverless
// schedule (EventBridge, Cloud Scheduler) where a long-running ticker would
// be wasteful.
func runSweep() {
	dataDir := flag.String("data", "data", "directory holding feedrig.db")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	conn, err := db.Open(filepath.Join(*dataDir, "feedrig.db"))
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	sw := &ttl.Sweeper{
		DB:       conn,
		Videos:   video.NewStore(conn),
		Settings: settings.NewStore(conn),
		Log:      log,
	}
	res, err := sw.SweepOnce(context.Background())
	if err != nil {
		log.Error("sweep", "err", err)
		os.Exit(1)
	}
	log.Info("sweep done", "aged", res.Aged, "archived", res.Archived, "purged", res.Purged)
}

// runPoll fetches new videos for one creator and exits. Useful for cron
// per-creator schedules or for a "force poll this one now" admin path.
func runPoll() {
	dataDir := flag.String("data", "data", "directory holding feedrig.db")
	mediaDir := flag.String("media", "media", "directory for downloaded videos")
	cookies := flag.String("cookies", "", "optional yt-dlp cookies file")
	chromePath := flag.String("chrome", "", "path to chromium binary")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: feedrig poll [flags] <handle>")
		os.Exit(2)
	}
	handle := flag.Arg(0)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	conn, err := db.Open(filepath.Join(*dataDir, "feedrig.db"))
	if err != nil {
		log.Error("open db", "err", err); os.Exit(1)
	}
	defer conn.Close()

	mediaAbs, _ := filepath.Abs(*mediaDir)
	creators := creator.NewStore(conn)
	videos := video.NewStore(conn)

	disc := buildDiscoverer("auto", *chromePath, log)
	svc := ingest.NewService(disc, ingest.YtDlpDownloader{CookieFile: *cookies}, creators, videos, mediaAbs, log)

	// Look up by handle. If absent, register first so the IDs are stable.
	cs, _ := creators.List(context.Background(), creator.SortHandle)
	var c *creator.Creator
	for i := range cs {
		if cs[i].Handle == handle {
			c = &cs[i]
			break
		}
	}
	if c == nil {
		newC, err := creators.Add(context.Background(), handle, "")
		if err != nil {
			log.Error("add creator", "err", err); os.Exit(1)
		}
		c = newC
	}
	added, err := svc.FetchNewForCreator(context.Background(), c)
	if err != nil {
		log.Error("poll", "err", err); os.Exit(1)
	}
	log.Info("poll done", "handle", handle, "added", added)
}

// runEnrich runs the transcribe→summarize→tag pipeline on one video
// synchronously and exits. The `--summarizer` / `--ollama-*` /
// `--openrouter-*` flags from the long-running mode are reused.
func runEnrich() {
	dataDir := flag.String("data", "data", "directory holding feedrig.db")
	summarizer := flag.String("summarizer", "auto", "summarizer: auto|ollama|openrouter|stub|none")
	ollamaURL := flag.String("ollama-url", "http://localhost:11434", "Ollama base URL")
	ollamaModel := flag.String("ollama-model", "llama3.2:3b", "Ollama model id")
	openrouterModel := flag.String("openrouter-model", "anthropic/claude-3.5-haiku", "OpenRouter model id")
	whisperModel := flag.String("whisper-model", "", "path to whisper.cpp model")
	categoriesFlag := flag.String("categories", "news,political-commentary,music,bass-guitar,baking,pizza,food,comedy,tech", "category menu for the summarizer")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: feedrig enrich [flags] <video-id>")
		os.Exit(2)
	}
	id, err := strconv.ParseInt(flag.Arg(0), 10, 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, "video-id must be an integer")
		os.Exit(2)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	conn, err := db.Open(filepath.Join(*dataDir, "feedrig.db"))
	if err != nil {
		log.Error("open db", "err", err); os.Exit(1)
	}
	defer conn.Close()

	worker := &enrich.Worker{
		Videos:      video.NewStore(conn),
		Enrich:      enrich.NewStore(conn),
		Transcriber: buildTranscriber(*whisperModel),
		Summarizer:  buildSummarizer(*summarizer, *ollamaURL, *ollamaModel, *openrouterModel, log),
		Categories:  splitCSV(*categoriesFlag),
		Log:         log,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	worker.ProcessOne(ctx, id)
	log.Info("enrich done", "video_id", id)
}

// runDigest renders the digest for one group and either prints it to stdout
// or delivers it via SMTP. Cron-friendly: `feedrig digest news --to me@x` to
// send a daily email.
func runDigest() {
	dataDir := flag.String("data", "data", "directory holding feedrig.db")
	baseURL := flag.String("base-url", "http://localhost:7777", "URL prefix for in-email video links")
	onlyUnseen := flag.Bool("only-unseen", false, "include only items posted since the group's last visit")
	to := flag.String("to", "", "comma-separated recipient list; empty = print to stdout")
	smtpHost := flag.String("smtp-host", os.Getenv("FEEDRIG_SMTP_HOST"), "SMTP host (FEEDRIG_SMTP_HOST)")
	smtpPort := flag.Int("smtp-port", envInt("FEEDRIG_SMTP_PORT", 587), "SMTP port (FEEDRIG_SMTP_PORT)")
	smtpUser := flag.String("smtp-user", os.Getenv("FEEDRIG_SMTP_USER"), "SMTP user (FEEDRIG_SMTP_USER)")
	smtpPass := flag.String("smtp-pass", os.Getenv("FEEDRIG_SMTP_PASS"), "SMTP password (FEEDRIG_SMTP_PASS)")
	smtpFrom := flag.String("smtp-from", os.Getenv("FEEDRIG_SMTP_FROM"), "SMTP from address (FEEDRIG_SMTP_FROM)")
	smtpTLS := flag.Bool("smtp-tls", false, "use implicit TLS (port 465 style) instead of STARTTLS")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: feedrig digest [flags] <group-slug>")
		os.Exit(2)
	}
	slug := flag.Arg(0)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	conn, err := db.Open(filepath.Join(*dataDir, "feedrig.db"))
	if err != nil {
		log.Error("open db", "err", err); os.Exit(1)
	}
	defer conn.Close()

	gr := groups.NewStore(conn)
	g, err := gr.GetBySlug(context.Background(), slug)
	if err != nil {
		log.Error("group lookup", "slug", slug, "err", err); os.Exit(1)
	}
	r := &digest.Renderer{
		Creators: creator.NewStore(conn),
		Videos:   video.NewStore(conn),
		Enrich:   enrich.NewStore(conn),
		Groups:   gr,
		BaseURL:  *baseURL,
	}
	msg, err := r.Render(context.Background(), g, *onlyUnseen)
	if err != nil {
		log.Error("render", "err", err); os.Exit(1)
	}

	if *to == "" {
		// Print mode: dump the text body. Useful for cron+pipe to mail(1)
		// or just for inspection.
		fmt.Print(msg.TextBody)
		return
	}

	msg.To = splitCSV(*to)
	mailer := notify.SMTPMailer{
		Host:           *smtpHost,
		Port:           *smtpPort,
		User:           *smtpUser,
		Pass:           *smtpPass,
		From:           *smtpFrom,
		UseImplicitTLS: *smtpTLS,
	}
	if err := mailer.Send(context.Background(), msg); err != nil {
		log.Error("send", "err", err); os.Exit(1)
	}
	log.Info("digest delivered", "group", slug, "recipients", len(msg.To), "items_subject", msg.Subject)
}

func envInt(name string, fallback int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// runMCP is the stdio MCP-server subcommand. Reads JSON-RPC from stdin and
// writes responses to stdout, sharing the SQLite DB with the web server.
// Logging goes to stderr so it doesn't pollute the protocol channel.
func runMCP() {
	dataDir := flag.String("data", "data", "directory holding feedrig.db")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	conn, err := db.Open(filepath.Join(*dataDir, "feedrig.db"))
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	srv := &mcp.Server{
		Creators: creator.NewStore(conn),
		Videos:   video.NewStore(conn),
		Enrich:   enrich.NewStore(conn),
		Groups:   groups.NewStore(conn),
		Log:      log,
	}
	if err := srv.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Error("mcp serve", "err", err)
		os.Exit(1)
	}
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
