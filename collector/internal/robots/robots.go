// Package robots reads robots.txt files (RFC 9309) and answers whether a crawler may fetch a path.
//
// Pricewise follows the standard strictly: the group for the most specific user agent that names
// the crawler applies (or the "*" group), the longest matching rule wins, Allow wins a tie, and "*"
// and "$" work as wildcards. A robots.txt that cannot be fetched because of a server error means
// "fetch nothing"; one that does not exist (4xx) means "fetch anything".
package robots

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rules are the parts of a robots.txt that apply to one crawler.
type Rules struct {
	allow      []string
	disallow   []string
	CrawlDelay time.Duration // zero if the file sets none
	Sitemaps   []string
	// Whether the whole site is closed because robots.txt could not be read.
	unreachable bool
}

// AllowAll is what a missing robots.txt means.
func AllowAll() *Rules { return &Rules{} }

// DisallowAll is what an unreachable robots.txt means.
func DisallowAll() *Rules { return &Rules{unreachable: true} }

type group struct {
	agents     []string
	allow      []string
	disallow   []string
	crawlDelay time.Duration
	hasDelay   bool
}

// Parse reads a robots.txt for the crawler whose product token is agent (e.g. "PricewiseBot").
func Parse(body string, agent string) *Rules {
	var groups []*group
	var sitemaps []string
	var cur *group
	lastWasAgent := false
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "user-agent":
			if !lastWasAgent || cur == nil {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(value))
			lastWasAgent = true
			continue
		case "allow", "disallow":
			if cur != nil {
				if key == "allow" {
					cur.allow = append(cur.allow, value)
				} else if value != "" { // an empty Disallow allows everything
					cur.disallow = append(cur.disallow, value)
				}
			}
		case "crawl-delay":
			if cur != nil {
				if s, err := strconv.ParseFloat(value, 64); err == nil && s >= 0 {
					cur.crawlDelay = time.Duration(s * float64(time.Second))
					cur.hasDelay = true
				}
			}
		case "sitemap":
			sitemaps = append(sitemaps, value)
		}
		lastWasAgent = false
	}

	// The group naming the crawler wins over "*"; all groups naming it are merged.
	name := strings.ToLower(agent)
	pick := func(match func(string) bool) *Rules {
		r := &Rules{Sitemaps: sitemaps}
		found := false
		for _, g := range groups {
			for _, a := range g.agents {
				if match(a) {
					found = true
					r.allow = append(r.allow, g.allow...)
					r.disallow = append(r.disallow, g.disallow...)
					if g.hasDelay {
						r.CrawlDelay = g.crawlDelay
					}
					break
				}
			}
		}
		if !found {
			return nil
		}
		return r
	}
	if r := pick(func(a string) bool { return a == name }); r != nil {
		return r
	}
	if r := pick(func(a string) bool { return a == "*" }); r != nil {
		return r
	}
	return &Rules{Sitemaps: sitemaps}
}

// Allowed says whether the path (with its query) may be fetched.
func (r *Rules) Allowed(path string) bool {
	if r.unreachable {
		return false
	}
	if path == "" {
		path = "/"
	}
	if path == "/robots.txt" {
		return true
	}
	best, allowed := -1, true
	for _, p := range r.allow {
		if n := matchLen(p, path); n > best || (n == best && n >= 0) {
			best, allowed = n, true
		}
	}
	for _, p := range r.disallow {
		if n := matchLen(p, path); n > best {
			best, allowed = n, false
		}
	}
	return allowed
}

// matchLen is the length of the rule if it matches the path, or -1.
func matchLen(pattern, path string) int {
	if pattern == "" {
		return -1
	}
	if wildcard(pattern, path) {
		return len(pattern)
	}
	return -1
}

// wildcard matches a robots.txt path pattern: a prefix match where "*" is any run of characters
// and a final "$" anchors the end.
func wildcard(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for i, part := range parts[1:] {
		last := i == len(parts)-2
		if last && anchored {
			return strings.HasSuffix(path[pos:], part)
		}
		j := strings.Index(path[pos:], part)
		if j < 0 {
			return false
		}
		pos += j + len(part)
	}
	return !anchored || pos == len(path)
}

// Cache fetches each host's robots.txt once and keeps it for a day.
type Cache struct {
	Client *http.Client
	Agent  string // the product token, e.g. "PricewiseBot"
	UA     string // the full User-Agent header
	TTL    time.Duration

	mu      sync.Mutex
	entries map[string]entry
	Fetches int // how many robots.txt files were downloaded
}

type entry struct {
	rules   *Rules
	fetched time.Time
}

// For returns the rules for the host of u.
func (c *Cache) For(ctx context.Context, u *url.URL) *Rules {
	origin := u.Scheme + "://" + u.Host
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]entry{}
	}
	e, ok := c.entries[origin]
	ttl := c.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	if ok && time.Since(e.fetched) < ttl {
		c.mu.Unlock()
		return e.rules
	}
	c.mu.Unlock()

	rules := c.fetch(ctx, origin)
	c.mu.Lock()
	c.entries[origin] = entry{rules, time.Now()}
	c.Fetches++
	c.mu.Unlock()
	return rules
}

func (c *Cache) fetch(ctx context.Context, origin string) *Rules {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return DisallowAll()
	}
	req.Header.Set("User-Agent", c.UA)
	resp, err := c.Client.Do(req)
	if err != nil {
		return DisallowAll()
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024)) // RFC 9309: at least 500 KiB
		if err != nil {
			return DisallowAll()
		}
		return Parse(string(body), c.Agent)
	case resp.StatusCode == http.StatusTooManyRequests:
		return DisallowAll() // asked to slow down: treated like a server error
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return AllowAll()
	default:
		return DisallowAll()
	}
}

func (r *Rules) String() string {
	return fmt.Sprintf("allow=%v disallow=%v delay=%v", r.allow, r.disallow, r.CrawlDelay)
}
