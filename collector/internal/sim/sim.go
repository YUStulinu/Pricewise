// Package sim serves the simulated shops that the demo and the evaluations crawl. Product titles
// are real offers from the WDC Products benchmark; the shops and the prices are simulated (see
// ml/pricewise_ml/simulate.py, which writes the catalogue this package serves).
//
// Each shop behaves like a polite real one: a robots.txt with a crawl delay and private paths, a
// sitemap, product pages with schema.org JSON-LD (marked up a little differently by each shop),
// a request limit enforced with 429 Too Many Requests, and the odd 503. The simulation's clock is
// a day number that the collector's replay mode advances; pages carry that day in their Date header.
package sim

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YUStulinu/Pricewise/collector/internal/ratelimit"
	"github.com/YUStulinu/Pricewise/collector/internal/robots"
)

type Shop struct {
	Name        string  `json:"name"`
	Port        int     `json:"port"`
	CrawlDelay  float64 `json:"crawl_delay"`
	Rate        float64 `json:"rate"`
	DisallowBot bool    `json:"disallow_bot"`
}

type Listing struct {
	ID          string     `json:"id"`
	Shop        int        `json:"shop"`
	Product     int        `json:"product"`
	Title       string     `json:"title"`
	Brand       *string    `json:"brand"`
	Description string     `json:"description"`
	Prices      []float64  `json:"prices"`
	Reference   []*float64 `json:"reference"`
}

type Catalogue struct {
	Start    string    `json:"start"`
	Days     int       `json:"days"`
	Currency string    `json:"currency"`
	Shops    []Shop    `json:"shops"`
	Listings []Listing `json:"listings"`
}

func Load(path string) (*Catalogue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Catalogue
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Stats are what the shops saw, for checking the collector's manners.
type Stats struct {
	Requests        int `json:"requests"`
	TooManyRequests int `json:"too_many_requests"`
	Unavailable     int `json:"unavailable"`
	// Requests by the collector for paths its robots.txt forbids it.
	RobotsViolations int `json:"robots_violations"`
}

type Server struct {
	cat   *Catalogue
	start time.Time
	day   atomic.Int64
	// FailPercent of product page requests answer 503 (deterministic per day and page).
	FailPercent int

	mu    sync.Mutex
	stats map[string]*Stats
}

func NewServer(cat *Catalogue) (*Server, error) {
	start, err := time.Parse("2006-01-02", cat.Start)
	if err != nil {
		return nil, err
	}
	return &Server{cat: cat, start: start, FailPercent: 1, stats: map[string]*Stats{}}, nil
}

func (s *Server) Day() int     { return int(s.day.Load()) }
func (s *Server) SetDay(d int) { s.day.Store(int64(d)) }
func (s *Server) date() time.Time {
	return s.start.AddDate(0, 0, s.Day()).Add(12 * time.Hour)
}

func (s *Server) Stats() map[string]Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Stats{}
	for k, v := range s.stats {
		out[k] = *v
	}
	return out
}

func (s *Server) count(shop string, f func(*Stats)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.stats[shop]
	if !ok {
		st = &Stats{}
		s.stats[shop] = st
	}
	f(st)
}

// RobotsTxt is what shop i serves at /robots.txt.
func (s *Server) RobotsTxt(i int, base string) string {
	sh := s.cat.Shops[i]
	var b strings.Builder
	if sh.DisallowBot {
		b.WriteString("User-agent: PricewiseBot\nDisallow: /\n\n")
	}
	b.WriteString("User-agent: *\nDisallow: /cart\nDisallow: /account/\nDisallow: /p/*/reviews\n")
	if sh.CrawlDelay > 0 {
		fmt.Fprintf(&b, "Crawl-delay: %g\n", sh.CrawlDelay)
	}
	fmt.Fprintf(&b, "\nSitemap: %s/sitemap.xml\n", base)
	return b.String()
}

// Handler serves shop i, whose address is base (e.g. http://127.0.0.1:9101).
func (s *Server) Handler(i int, base string) http.Handler {
	sh := s.cat.Shops[i]
	listings := map[string]*Listing{}
	var ids []string
	for k := range s.cat.Listings {
		l := &s.cat.Listings[k]
		if l.Shop == i {
			listings[l.ID] = l
			ids = append(ids, l.ID)
		}
	}
	robotsTxt := s.RobotsTxt(i, base)
	rules := robots.Parse(robotsTxt, "PricewiseBot")
	rate := sh.Rate
	if sh.CrawlDelay > 0 && 1/sh.CrawlDelay < rate {
		rate = 1 / sh.CrawlDelay
	}
	// A little slack over the announced rate, as real limits have.
	limit := ratelimit.NewBucket(rate*1.1, 3)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.count(sh.Name, func(st *Stats) { st.Requests++ })
		w.Header().Set("Date", s.date().UTC().Format(http.TimeFormat))
		if strings.Contains(r.UserAgent(), "PricewiseBot") && !rules.Allowed(r.URL.RequestURI()) {
			s.count(sh.Name, func(st *Stats) { st.RobotsViolations++ })
		}
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, robotsTxt)
			return
		}
		if !limit.Allow() {
			s.count(sh.Name, func(st *Stats) { st.TooManyRequests++ })
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		switch {
		case r.URL.Path == "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`+"\n")
			for _, id := range ids {
				fmt.Fprintf(w, "<url><loc>%s/p/%s</loc></url>\n", base, id)
			}
			fmt.Fprint(w, "</urlset>\n")
		case strings.HasPrefix(r.URL.Path, "/p/"):
			l, ok := listings[strings.TrimPrefix(r.URL.Path, "/p/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if s.fails(l.ID) {
				s.count(sh.Name, func(st *Stats) { st.Unavailable++ })
				http.Error(w, "try later", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, s.page(i, base, l))
		case r.URL.Path == "/":
			fmt.Fprintf(w, "<html><body><h1>%s</h1><p>A simulated shop for Pricewise.</p></body></html>", html.EscapeString(sh.Name))
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *Server) fails(id string) bool {
	if s.FailPercent <= 0 {
		return false
	}
	h := fnv.New32a()
	fmt.Fprintf(h, "%d/%s", s.Day(), id)
	return int(h.Sum32()%100) < s.FailPercent
}

// Prices of a listing on the current day.
func (s *Server) prices(l *Listing) (price float64, reference float64) {
	d := s.Day()
	if d >= len(l.Prices) {
		d = len(l.Prices) - 1
	}
	if d < 0 {
		d = 0
	}
	price = l.Prices[d]
	if d < len(l.Reference) && l.Reference[d] != nil {
		reference = *l.Reference[d]
	}
	return
}

// page writes a product page. Each shop marks it up its own way, as real shops do.
func (s *Server) page(i int, base string, l *Listing) string {
	price, ref := s.prices(l)
	cur := s.cat.Currency
	url := base + "/p/" + l.ID
	brand := ""
	if l.Brand != nil {
		brand = *l.Brand
	}
	money := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	strike := func(v float64) map[string]any {
		return map[string]any{"@type": "UnitPriceSpecification", "priceType": "https://schema.org/StrikethroughPrice",
			"price": money(v), "priceCurrency": cur}
	}
	offer := map[string]any{"@type": "Offer", "url": url, "priceCurrency": cur, "availability": "https://schema.org/InStock"}
	product := map[string]any{"@context": "https://schema.org", "@type": "Product", "name": l.Title, "sku": l.ID,
		"description": l.Description}
	if brand != "" {
		product["brand"] = map[string]any{"@type": "Brand", "name": brand}
	}
	var doc any = product
	switch i % 5 {
	case 0: // price as a string, "was" price as a strikethrough specification
		offer["price"] = money(price)
		if ref > 0 {
			offer["priceSpecification"] = []any{strike(ref)}
		}
		product["offers"] = offer
	case 1: // inside an @graph, offers as an array
		offer["price"] = money(price)
		if ref > 0 {
			offer["priceSpecification"] = strike(ref)
		}
		product["offers"] = []any{offer}
		delete(product, "@context")
		doc = map[string]any{"@context": "https://schema.org", "@graph": []any{
			map[string]any{"@type": "WebPage", "url": url}, product}}
	case 2: // price as a number
		offer["price"] = price
		if ref > 0 {
			offer["priceSpecification"] = []any{strike(ref)}
		}
		product["offers"] = offer
	case 3: // the "was" price as a list price
		offer["price"] = money(price)
		if ref > 0 {
			offer["priceSpecification"] = map[string]any{"@type": "UnitPriceSpecification",
				"priceType": "https://schema.org/ListPrice", "price": ref, "priceCurrency": cur}
		}
		product["offers"] = offer
	default: // price only inside a price specification, with a decimal comma
		specs := []any{map[string]any{"@type": "UnitPriceSpecification", "price": strings.Replace(money(price), ".", ",", 1),
			"priceCurrency": cur}}
		if ref > 0 {
			specs = append(specs, strike(ref))
		}
		offer["priceSpecification"] = specs
		product["offers"] = offer
	}
	ld, _ := json.Marshal(doc)
	was := ""
	if ref > 0 {
		was = fmt.Sprintf(` <s>%s %s</s>`, money(ref), cur)
	}
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>%s | %s</title>
<script type="application/ld+json">%s</script></head>
<body><h1>%s</h1><p class="price">%s %s%s</p><p>%s</p><a href="/cart">Add to cart</a> <a href="/p/%s/reviews">Reviews</a></body></html>`,
		html.EscapeString(l.Title), html.EscapeString(s.cat.Shops[i].Name), ld, html.EscapeString(l.Title), money(price), cur, was,
		html.EscapeString(l.Description), l.ID)
}

// Admin serves the simulation's controls: GET/POST /_sim/day and GET /_sim/stats.
func (s *Server) Admin() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/_sim/day", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			d, err := strconv.Atoi(r.URL.Query().Get("d"))
			if err != nil || d < 0 || d >= s.cat.Days {
				http.Error(w, "d must be a day of the simulation", http.StatusBadRequest)
				return
			}
			s.SetDay(d)
		}
		json.NewEncoder(w).Encode(map[string]any{"day": s.Day(), "date": s.date().Format("2006-01-02"), "days": s.cat.Days})
	})
	mux.HandleFunc("/_sim/stats", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(s.Stats())
	})
	return mux
}
