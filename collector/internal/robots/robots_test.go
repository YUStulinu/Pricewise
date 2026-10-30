package robots

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

const sample = `
# comment
User-agent: *
Disallow: /cart
Disallow: /search?
Allow: /search?page=
Crawl-delay: 2

User-agent: BadBot
User-agent: PricewiseBot
Disallow: /p/*/reviews
Disallow: /*.pdf$
Allow: /p/
Crawl-delay: 0.5

Sitemap: https://shop.example/sitemap.xml
`

func TestTheNamedGroupWins(t *testing.T) {
	r := Parse(sample, "PricewiseBot")
	cases := map[string]bool{
		"/p/123":         true,
		"/p/123/reviews": false,
		"/manual.pdf":    false,
		"/manual.pdf?x":  true, // "$" anchors the end
		"/cart":          true, // the "*" group does not apply to a named crawler
		"/robots.txt":    true,
	}
	for path, want := range cases {
		if got := r.Allowed(path); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
	if r.CrawlDelay != 500*time.Millisecond {
		t.Errorf("crawl delay %v", r.CrawlDelay)
	}
	if len(r.Sitemaps) != 1 {
		t.Errorf("sitemaps %v", r.Sitemaps)
	}
}

func TestTheStarGroupAndLongestMatch(t *testing.T) {
	r := Parse(sample, "OtherBot")
	cases := map[string]bool{
		"/cart":           false,
		"/cart/items":     false,
		"/search?q=ssd":   false,
		"/search?page=2":  true, // the longer Allow wins
		"/p/123/reviews":  true,
		"/":               true,
	}
	for path, want := range cases {
		if got := r.Allowed(path); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
	if r.CrawlDelay != 2*time.Second {
		t.Errorf("crawl delay %v", r.CrawlDelay)
	}
}

func TestAllowWinsATie(t *testing.T) {
	r := Parse("User-agent: *\nDisallow: /a\nAllow: /a\n", "x")
	if !r.Allowed("/a") {
		t.Error("an Allow as long as the Disallow should win")
	}
}

func TestDisallowEverythingForOneBot(t *testing.T) {
	r := Parse("User-agent: PricewiseBot\nDisallow: /\n\nUser-agent: *\nAllow: /\n", "PricewiseBot")
	if r.Allowed("/p/1") || r.Allowed("/") {
		t.Error("PricewiseBot is shut out")
	}
}

func TestCacheFetchesOnceAndTreatsErrorsStrictly(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("User-agent: *\nDisallow: /private\n"))
	}))
	defer srv.Close()
	c := &Cache{Client: srv.Client(), Agent: "PricewiseBot", UA: "PricewiseBot/test"}
	u, _ := url.Parse(srv.URL + "/p/1")
	for i := 0; i < 3; i++ {
		if !c.For(context.Background(), u).Allowed("/p/1") {
			t.Fatal("allowed path refused")
		}
	}
	if hits != 1 {
		t.Errorf("robots.txt fetched %d times", hits)
	}

	for status, allowed := range map[int]bool{404: true, 500: false, 503: false, 429: false} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		c := &Cache{Client: s.Client(), Agent: "PricewiseBot"}
		u, _ := url.Parse(s.URL + "/p/1")
		if got := c.For(context.Background(), u).Allowed("/p/1"); got != allowed {
			t.Errorf("robots.txt status %d: allowed=%v, want %v", status, got, allowed)
		}
		s.Close()
	}
}
