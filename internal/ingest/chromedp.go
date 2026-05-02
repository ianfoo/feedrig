package ingest

import (
	"bufio"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	cdp "github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ChromedpDiscoverer drives a headless Chromium against a creator's profile
// page and harvests post shortcodes from the rendered DOM. Slower than a
// direct API call (~5–15s/profile) but resilient to Instagram's HTML churn
// because we only inspect <a href="/p/SHORTCODE/"> and /reel/ links.
//
// Built-in pacing: sleeps with jitter between actions to look less
// botlike — this is for politeness, not detection-evasion.
type ChromedpDiscoverer struct {
	// ChromePath optionally overrides the chromium binary location.
	ChromePath string
	// Timeout caps a single discovery run. Defaults to 30s.
	Timeout time.Duration
	// MaxScrolls bounds how aggressively we scroll the profile to load more
	// posts. Each scroll typically loads ~12 more posts. Default 0 = no scroll
	// (capture only what's above the fold; ~12 posts).
	MaxScrolls int
	// PacingMin / PacingMax bound the randomized think-time between scroll
	// actions. Defaults: 1.5s–4s. Set both to zero to disable pacing (faster
	// but more visibly botlike).
	PacingMin, PacingMax time.Duration
	// CookieFile is a yt-dlp / curl-compatible Netscape-format cookies.txt.
	// When set, the listed cookies are injected before navigating to
	// instagram.com so the profile loads as if a logged-in user is viewing.
	// Same file format used by --cookies for yt-dlp.
	CookieFile string
}

func (d ChromedpDiscoverer) pacingRange() (time.Duration, time.Duration) {
	mn, mx := d.PacingMin, d.PacingMax
	if mn <= 0 {
		mn = 1500 * time.Millisecond
	}
	if mx <= 0 {
		mx = 4 * time.Second
	}
	if mx < mn {
		mx = mn
	}
	return mn, mx
}

// shortcodeAnchorRE matches the path of post anchors in the rendered DOM.
var shortcodeAnchorRE = regexp.MustCompile(`/(?:p|reel|tv)/([A-Za-z0-9_-]+)/?`)

// loadNetscapeCookies parses a yt-dlp / curl-compatible cookies.txt file
// (Netscape HTTP Cookie File format) into chromedp's CookieParam shape.
// Lines starting with "#" are comments; "#HttpOnly_" prefix on a domain is
// a Mozilla extension that we treat as a regular cookie with HttpOnly=true.
//
// Format (tab-separated):
//   domain  domain_specified  path  secure  expiry  name  value
func loadNetscapeCookies(path string) ([]*network.CookieParam, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []*network.CookieParam
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		httpOnly := false
		if strings.HasPrefix(trim, "#HttpOnly_") {
			httpOnly = true
			trim = strings.TrimPrefix(trim, "#HttpOnly_")
		} else if strings.HasPrefix(trim, "#") {
			continue
		}
		fields := strings.Split(trim, "\t")
		if len(fields) < 7 {
			continue
		}
		domain := fields[0]
		path := fields[2]
		secure := strings.EqualFold(fields[3], "TRUE")
		var expires float64
		if exp, err := strconv.ParseInt(fields[4], 10, 64); err == nil && exp > 0 {
			expires = float64(exp)
		}
		c := &network.CookieParam{
			Name:     fields[5],
			Value:    fields[6],
			Domain:   domain,
			Path:     path,
			Secure:   secure,
			HTTPOnly: httpOnly,
		}
		if expires > 0 {
			cdpExp := cdpTimestampSeconds(expires)
			c.Expires = &cdpExp
		}
		out = append(out, c)
	}
	return out, scanner.Err()
}

// cdpTimestampSeconds — chromedp expects unix-seconds-as-float for
// CookieParam.Expires. Wrap as a cdp.TimeSinceEpoch so the type aligns.
func cdpTimestampSeconds(unix float64) cdp.TimeSinceEpoch {
	return cdp.TimeSinceEpoch(time.Unix(int64(unix), 0))
}

// jitterSleep is a chromedp action that pauses for a random duration in
// [mn, mx]. Used between scrolls to look less botlike.
func jitterSleep(mn, mx time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		span := mx - mn
		if span <= 0 {
			return chromedp.Sleep(mn).Do(ctx)
		}
		d := mn + time.Duration(rand.Int64N(int64(span)))
		return chromedp.Sleep(d).Do(ctx)
	})
}

func (d ChromedpDiscoverer) Recent(ctx context.Context, handle string) ([]string, error) {
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15"),
	)
	if d.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(d.ChromePath))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	bctx, cancelB := chromedp.NewContext(allocCtx)
	defer cancelB()

	url := "https://www.instagram.com/" + handle + "/"
	var hrefs []string
	mn, mx := d.pacingRange()
	tasks := chromedp.Tasks{}
	if d.CookieFile != "" {
		cookies, err := loadNetscapeCookies(d.CookieFile)
		if err != nil {
			return nil, fmt.Errorf("load cookies: %w", err)
		}
		// Inject cookies before any navigation so the first request to
		// instagram.com already carries the session.
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			return network.SetCookies(cookies).Do(ctx)
		}))
	}
	tasks = append(tasks,
		chromedp.Navigate(url),
		jitterSleep(mn, mx),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('a[href*="/p/"], a[href*="/reel/"], a[href*="/tv/"]')).map(a => a.getAttribute('href'))`, &hrefs),
	)
	for i := 0; i < d.MaxScrolls; i++ {
		tasks = append(tasks,
			chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
			jitterSleep(mn, mx),
			chromedp.Evaluate(`Array.from(document.querySelectorAll('a[href*="/p/"], a[href*="/reel/"], a[href*="/tv/"]')).map(a => a.getAttribute('href'))`, &hrefs),
		)
	}

	if err := chromedp.Run(bctx, tasks); err != nil {
		return nil, fmt.Errorf("chromedp: %w", err)
	}

	seen := make(map[string]struct{})
	var codes []string
	for _, href := range hrefs {
		// Skip "Login" / "Like this post" interstitials by checking for shortcode shape.
		m := shortcodeAnchorRE.FindStringSubmatch(href)
		if len(m) < 2 {
			continue
		}
		code := m[1]
		// Defensive: shortcodes are typically 8–12 chars; reject anything wildly off.
		if len(code) < 5 || len(code) > 30 {
			continue
		}
		// Skip the literal handle if it slipped through (e.g. "/p/handle/"):
		if strings.EqualFold(code, handle) {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes, nil
}
