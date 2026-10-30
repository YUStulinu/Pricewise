package crawler

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YUStulinu/Pricewise/collector/internal/sim"
)

type memory struct {
	mu  sync.Mutex
	obs []Observation
}

func (m *memory) Save(_ context.Context, o Observation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.obs = append(m.obs, o)
	return nil
}

func ptr(v float64) *float64 { return &v }

// catalogue: shop 0 is fast, shop 1 has a hidden limit of 5 requests a second, shop 2 shuts
// PricewiseBot out in robots.txt.
func catalogue() *sim.Catalogue {
	c := &sim.Catalogue{Start: "2026-06-01", Days: 3, Currency: "EUR", Shops: []sim.Shop{
		{Name: "Fast", Rate: 200},
		{Name: "Strict", Rate: 5},
		{Name: "Closed", Rate: 200, DisallowBot: true},
	}}
	for shop := 0; shop < 3; shop++ {
		for i := 0; i < 12; i++ {
			c.Listings = append(c.Listings, sim.Listing{
				ID: fmt.Sprintf("%d-%d", shop, i), Shop: shop, Product: i, Title: fmt.Sprintf("Product %d", i),
				Prices: []float64{10, 9, 8}, Reference: []*float64{nil, ptr(12), nil},
			})
		}
	}
	return c
}

func start(t *testing.T) (*sim.Server, []Site) {
	srv, err := sim.NewServer(catalogue())
	if err != nil {
		t.Fatal(err)
	}
	srv.FailPercent = 0
	var sites []Site
	for i, name := range []string{"Fast", "Strict", "Closed"} {
		var base string
		ts := httptest.NewUnstartedServer(nil)
		ts.Start()
		base = ts.URL
		ts.Config.Handler = srv.Handler(i, base)
		t.Cleanup(ts.Close)
		sites = append(sites, Site{Name: name, BaseURL: base, Rate: 50, Simulated: true})
	}
	return srv, sites
}

func TestCrawlIsPoliteAndComplete(t *testing.T) {
	srv, sites := start(t)
	srv.SetDay(1)
	sink := &memory{}
	c := New(sink, 100)
	c.TrustDateHeader = true
	stats := c.Crawl(context.Background(), sites)

	got := srv.Stats()
	if got["Closed"].Requests > 1 { // only its robots.txt
		t.Errorf("the closed shop got %d requests", got["Closed"].Requests)
	}
	for name, s := range got {
		if s.RobotsViolations != 0 {
			t.Errorf("%s: %d requests robots.txt forbids", name, s.RobotsViolations)
		}
	}
	if len(sink.obs) != 24 {
		t.Fatalf("saved %d observations, want 24 (12 from each open shop)", len(sink.obs))
	}
	// The strict shop answered 429 at first; the crawler slowed down and still got every page.
	strict := strings.TrimPrefix(sites[1].BaseURL, "http://")
	if stats[strict].Saved != 12 || stats[strict].FinalRate >= 50 {
		t.Errorf("strict shop: %+v", stats[strict])
	}
	for _, o := range sink.obs {
		if o.Product.Price != 9 || o.Product.ReferencePrice != 12 || o.Product.Currency != "EUR" {
			t.Errorf("%s: %+v", o.URL, o.Product)
		}
		if o.ObservedAt.Format("2006-01-02") != "2026-06-02" {
			t.Errorf("observed at %v, want the simulated day 2026-06-02", o.ObservedAt)
		}
	}
}

func TestEveryMarkupVariantIsRead(t *testing.T) {
	c := catalogue()
	c.Shops = nil
	c.Listings = nil
	for i := 0; i < 5; i++ {
		c.Shops = append(c.Shops, sim.Shop{Name: fmt.Sprintf("S%d", i), Rate: 100})
		c.Listings = append(c.Listings, sim.Listing{ID: fmt.Sprint(i), Shop: i, Title: "Thing", Prices: []float64{1299.5}, Reference: []*float64{ptr(1499)}})
	}
	srv, _ := sim.NewServer(c)
	srv.FailPercent = 0
	var sites []Site
	for i := range c.Shops {
		ts := httptest.NewServer(nil)
		ts.Config.Handler = srv.Handler(i, ts.URL)
		defer ts.Close()
		sites = append(sites, Site{Name: c.Shops[i].Name, BaseURL: ts.URL, Rate: 50, Simulated: true})
	}
	sink := &memory{}
	New(sink, 10).Crawl(context.Background(), sites)
	if len(sink.obs) != 5 {
		t.Fatalf("read %d of 5 markup variants", len(sink.obs))
	}
	for _, o := range sink.obs {
		if o.Product.Price != 1299.5 || o.Product.ReferencePrice != 1499 {
			t.Errorf("%s: %+v", o.Site.Name, o.Product)
		}
	}
}

func TestRealSitesNeedCheckedTerms(t *testing.T) {
	if err := (Site{Name: "x", BaseURL: "https://shop.example"}).Check(); err == nil {
		t.Error("a real site with no terms check was accepted")
	}
	if err := (Site{Name: "x", BaseURL: "https://shop.example", Simulated: true}).Check(); err == nil {
		t.Error("a real address marked simulated was accepted")
	}
	s := Site{Name: "x", BaseURL: "https://shop.example"}
	s.TermsChecked = &struct {
		By   string `json:"by"`
		On   string `json:"on"`
		Note string `json:"note"`
	}{By: "me", On: "2026-10-30"}
	if err := s.Check(); err != nil {
		t.Error(err)
	}
	if err := (Site{Name: "x", BaseURL: "http://127.0.0.1:9101", Simulated: true}).Check(); err != nil {
		t.Error(err)
	}
}

func TestRetryAfter(t *testing.T) {
	if retryAfter("3") != 3*time.Second || retryAfter("") != time.Second || retryAfter("9999") != time.Minute {
		t.Error("Retry-After not read")
	}
}
