// Package crawler collects product prices from the sites on the allowlist.
//
// A pool of workers (hundreds, if asked) shares one queue of pages across all sites; each request
// first waits for its host's token bucket, so the pool can be large while every shop sees no more
// than its own rate. Before a site is touched its robots.txt is read, and no page it forbids is
// ever requested; a crawl delay there lowers the rate. Pages come from the site's sitemap only:
// the crawler does not follow links. A 429 answer slows the host down and honours Retry-After.
package crawler

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YUStulinu/Pricewise/collector/internal/extract"
	"github.com/YUStulinu/Pricewise/collector/internal/ratelimit"
	"github.com/YUStulinu/Pricewise/collector/internal/robots"
)

const (
	Agent = "PricewiseBot"
	UA    = "PricewiseBot/0.1 (+https://github.com/YUStulinu/Pricewise; price comparison; respects robots.txt)"
)

// Site is one entry of the allowlist.
type Site struct {
	Name    string  `json:"name"`
	BaseURL string  `json:"base_url"`
	Rate    float64 `json:"rate"` // requests a second at most; robots.txt may lower it
	Sitemap string  `json:"sitemap,omitempty"`
	// A real site may only be crawled after a person has read its terms of use and found that they
	// allow it; who did so and when is recorded here.
	TermsChecked *struct {
		By   string `json:"by"`
		On   string `json:"on"`
		Note string `json:"note"`
	} `json:"terms_checked,omitempty"`
	Simulated bool `json:"simulated,omitempty"`
}

// LoadSites reads an allowlist file and refuses real sites whose terms nobody has checked.
func LoadSites(path string) ([]Site, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Sites []Site `json:"sites"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, s := range file.Sites {
		if err := s.Check(); err != nil {
			return nil, err
		}
	}
	return file.Sites, nil
}

// Check refuses a site that may not be crawled.
func (s Site) Check() error {
	u, err := url.Parse(s.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("site %q: base_url %q is not an http(s) address", s.Name, s.BaseURL)
	}
	local := isLocal(u.Hostname())
	if s.Simulated && !local {
		return fmt.Errorf("site %q: only a local address can be marked simulated", s.Name)
	}
	if !local && (s.TermsChecked == nil || s.TermsChecked.By == "" || s.TermsChecked.On == "") {
		return fmt.Errorf("site %q: read its terms of use first, and record who checked them and when in terms_checked", s.Name)
	}
	return nil
}

func isLocal(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Observation is one product page as read at one time.
type Observation struct {
	Site       Site
	URL        string
	Product    extract.Product
	ObservedAt time.Time
}

// Sink stores observations (the database, or a slice in tests).
type Sink interface {
	Save(ctx context.Context, o Observation) error
}

// HostStats count what happened on one host during a crawl.
type HostStats struct {
	Pages           int           `json:"pages"`
	Saved           int           `json:"saved"`
	RobotsSkipped   int           `json:"robots_skipped"`
	TooManyRequests int           `json:"too_many_requests"`
	Errors          int           `json:"errors"`
	NoProduct       int           `json:"no_product"`
	FinalRate       float64       `json:"final_rate"`
	Took            time.Duration `json:"took"`
}

type Crawler struct {
	Client  *http.Client
	Robots  *robots.Cache
	Hosts   *ratelimit.Hosts
	Sink    Sink
	Workers int
	// Use the server's Date header as the observation time (the simulated shops' clock).
	TrustDateHeader bool
	// Retries of a page after a 429 or a 5xx.
	Retries int

	mu    sync.Mutex
	stats map[string]*HostStats
}

func New(sink Sink, workers int) *Crawler {
	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:     30 * time.Second,
		},
		// Redirects are followed only within the same host.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Host != via[0].URL.Host {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	return &Crawler{
		Client:  client,
		Robots:  &robots.Cache{Client: client, Agent: Agent, UA: UA},
		Hosts:   &ratelimit.Hosts{Default: 2},
		Sink:    sink,
		Workers: workers,
		Retries: 3,
	}
}

type job struct {
	site    Site
	url     *url.URL
	attempt int
}

func (c *Crawler) stat(host string, f func(*HostStats)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stats == nil {
		c.stats = map[string]*HostStats{}
	}
	s, ok := c.stats[host]
	if !ok {
		s = &HostStats{}
		c.stats[host] = s
	}
	f(s)
}

// Crawl reads every product page of every site once and returns what happened on each host.
func (c *Crawler) Crawl(ctx context.Context, sites []Site) map[string]HostStats {
	c.mu.Lock()
	c.stats = map[string]*HostStats{}
	c.mu.Unlock()
	started := time.Now()

	jobs := make(chan job, 1024)
	var pending sync.WaitGroup
	var workers sync.WaitGroup
	for i := 0; i < max(c.Workers, 1); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := range jobs {
				c.visit(ctx, j, jobs, &pending)
			}
		}()
	}

	// Discover each site's pages concurrently: one slow sitemap should not hold up the others.
	var discover sync.WaitGroup
	for _, s := range sites {
		discover.Add(1)
		go func(s Site) {
			defer discover.Done()
			urls, err := c.pages(ctx, s)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", s.Name, err)
				return
			}
			for _, u := range urls {
				pending.Add(1)
				select {
				case jobs <- job{site: s, url: u}:
				case <-ctx.Done():
					pending.Done()
					return
				}
			}
		}(s)
	}
	discover.Wait()
	pending.Wait()
	close(jobs)
	workers.Wait()

	out := map[string]HostStats{}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range sites {
		u, _ := url.Parse(s.BaseURL)
		st := c.stats[u.Host]
		if st == nil {
			st = &HostStats{}
		}
		st.FinalRate = c.Hosts.For(u.Host, s.Rate, 0).Rate()
		st.Took = time.Since(started).Round(time.Millisecond)
		out[u.Host] = *st
	}
	return out
}

// pages lists a site's product pages from its sitemap, keeping only those robots.txt allows.
func (c *Crawler) pages(ctx context.Context, s Site) ([]*url.URL, error) {
	base, err := url.Parse(s.BaseURL)
	if err != nil {
		return nil, err
	}
	rules := c.Robots.For(ctx, base)
	sitemaps := rules.Sitemaps
	if s.Sitemap != "" {
		sitemaps = []string{s.Sitemap}
	}
	if len(sitemaps) == 0 {
		sitemaps = []string{strings.TrimRight(s.BaseURL, "/") + "/sitemap.xml"}
	}
	var out []*url.URL
	for _, sm := range sitemaps {
		su, err := base.Parse(sm)
		if err != nil || su.Host != base.Host {
			continue // a sitemap on another host is not this site's to give
		}
		if !rules.Allowed(su.RequestURI()) {
			c.stat(base.Host, func(h *HostStats) { h.RobotsSkipped++ })
			continue
		}
		body, _, err := c.get(ctx, s, su, rules)
		if err != nil {
			return nil, fmt.Errorf("sitemap %s: %w", su, err)
		}
		var set struct {
			URLs []struct {
				Loc string `xml:"loc"`
			} `xml:"url"`
		}
		if err := xml.Unmarshal(body, &set); err != nil {
			return nil, fmt.Errorf("sitemap %s: %w", su, err)
		}
		for _, x := range set.URLs {
			u, err := base.Parse(strings.TrimSpace(x.Loc))
			if err != nil || u.Host != base.Host {
				continue
			}
			if !rules.Allowed(u.RequestURI()) {
				c.stat(base.Host, func(h *HostStats) { h.RobotsSkipped++ })
				continue
			}
			out = append(out, u)
		}
	}
	return out, nil
}

var errRetry = errors.New("retry later")

type statusError struct {
	status     int
	retryAfter time.Duration
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

// get fetches one URL politely: robots.txt checked, the host's bucket waited for. It returns the
// body and the time of the observation.
func (c *Crawler) get(ctx context.Context, s Site, u *url.URL, rules *robots.Rules) ([]byte, time.Time, error) {
	now := time.Now()
	if !rules.Allowed(u.RequestURI()) {
		return nil, now, fmt.Errorf("robots.txt forbids %s", u.RequestURI())
	}
	bucket := c.Hosts.For(u.Host, s.Rate, rules.CrawlDelay)
	if err := bucket.Wait(ctx); err != nil {
		return nil, now, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, now, err
	}
	req.Header.Set("User-Agent", UA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, now, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, now, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		e := &statusError{status: resp.StatusCode, retryAfter: retryAfter(resp.Header.Get("Retry-After"))}
		if resp.StatusCode == http.StatusTooManyRequests {
			bucket.Slow(0.5)
			c.stat(u.Host, func(h *HostStats) { h.TooManyRequests++ })
		}
		return nil, now, fmt.Errorf("%w: %w", errRetry, e)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, now, &statusError{status: resp.StatusCode}
	}
	if c.TrustDateHeader {
		if d, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
			now = d
		}
	}
	return body, now, nil
}

func retryAfter(h string) time.Duration {
	if h == "" {
		return time.Second
	}
	if s, err := strconv.Atoi(h); err == nil {
		return min(time.Duration(s)*time.Second, time.Minute)
	}
	if t, err := http.ParseTime(h); err == nil {
		return min(max(time.Until(t), 0), time.Minute)
	}
	return time.Second
}

func (c *Crawler) visit(ctx context.Context, j job, jobs chan<- job, pending *sync.WaitGroup) {
	host := j.url.Host
	rules := c.Robots.For(ctx, j.url)
	body, observed, err := c.get(ctx, j.site, j.url, rules)
	if err != nil {
		var se *statusError
		if errors.Is(err, errRetry) && errors.As(err, &se) && j.attempt < c.Retries && ctx.Err() == nil {
			// Retry after the wait the server asked for, without holding a worker.
			j.attempt++
			time.AfterFunc(se.retryAfter*time.Duration(j.attempt), func() {
				select {
				case jobs <- j:
				case <-ctx.Done():
					pending.Done()
				}
			})
			return
		}
		c.stat(host, func(h *HostStats) { h.Errors++ })
		pending.Done()
		return
	}
	defer pending.Done()
	c.stat(host, func(h *HostStats) { h.Pages++ })
	p, err := extract.FromHTML(strings.NewReader(string(body)))
	if err != nil {
		c.stat(host, func(h *HostStats) { h.NoProduct++ })
		return
	}
	if err := c.Sink.Save(ctx, Observation{Site: j.site, URL: j.url.String(), Product: p, ObservedAt: observed.UTC()}); err != nil {
		fmt.Fprintf(os.Stderr, "save %s: %v\n", j.url, err)
		c.stat(host, func(h *HostStats) { h.Errors++ })
		return
	}
	c.stat(host, func(h *HostStats) { h.Saved++ })
}
