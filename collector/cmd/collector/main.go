// Command collector reads product prices from the sites on an allowlist into the database.
//
//	collector crawl  -sites sites.json             one pass over every site
//	collector run    -sites sites.json -every 6h   a pass every interval, until stopped
//	collector replay -sites sim-sites.json -sim http://127.0.0.1:9100 -days 120
//
// replay drives the simulated shops (cmd/shopsim) through their days, one crawl a day, and dates
// each observation by the shop's simulated clock.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"time"

	"github.com/YUStulinu/Pricewise/collector/internal/crawler"
	"github.com/YUStulinu/Pricewise/collector/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: collector crawl|run|replay [flags]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	sitesPath := fs.String("sites", "sites.json", "the allowlist of sites")
	dbURL := fs.String("db", envOr("PRICEWISE_DB", "postgres://pricewise:pricewise@127.0.0.1:5433/pricewise"), "PostgreSQL address")
	workers := fs.Int("workers", 256, "concurrent requests in flight across all sites")
	every := fs.Duration("every", 6*time.Hour, "run: time between passes")
	simURL := fs.String("sim", "http://127.0.0.1:9100", "replay: the simulation's control address")
	days := fs.Int("days", 0, "replay: days to replay (0: all)")
	from := fs.Int("from", 0, "replay: first day")
	fs.Parse(os.Args[2:])

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	sites, err := crawler.LoadSites(*sitesPath)
	if err != nil {
		fail(err)
	}
	db, err := store.Open(ctx, *dbURL)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	c := crawler.New(db, *workers)

	switch cmd {
	case "crawl":
		report(c.Crawl(ctx, sites))
	case "run":
		for {
			report(c.Crawl(ctx, sites))
			// A little jitter so that passes do not fall on the same second every time.
			wait := *every + time.Duration(rand.Int64N(int64(*every/20+1)))
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	case "replay":
		c.TrustDateHeader = true
		total := simDays(*simURL)
		last := total
		if *days > 0 && *from+*days < total {
			last = *from + *days
		}
		for d := *from; d < last && ctx.Err() == nil; d++ {
			resp, err := http.Post(fmt.Sprintf("%s/_sim/day?d=%d", *simURL, d), "", nil)
			if err != nil {
				fail(err)
			}
			resp.Body.Close()
			t := time.Now()
			stats := c.Crawl(ctx, sites)
			saved, limited := 0, 0
			for _, s := range stats {
				saved += s.Saved
				limited += s.TooManyRequests
			}
			fmt.Printf("day %3d: %d prices saved in %v (%d answers 429)\n", d, saved, time.Since(t).Round(time.Millisecond), limited)
		}
		report(nil)
	default:
		fail(fmt.Errorf("unknown command %q", cmd))
	}
}

func simDays(url string) int {
	resp, err := http.Get(url + "/_sim/day")
	if err != nil {
		fail(fmt.Errorf("the shop simulation is not running at %s: %w", url, err))
	}
	defer resp.Body.Close()
	var v struct{ Days int }
	json.NewDecoder(resp.Body).Decode(&v)
	return v.Days
}

func report(stats map[string]crawler.HostStats) {
	if stats == nil {
		return
	}
	hosts := make([]string, 0, len(stats))
	for h := range stats {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		s := stats[h]
		fmt.Printf("%-22s pages %5d saved %5d robots-skipped %4d 429s %3d errors %3d rate %.1f/s\n",
			h, s.Pages, s.Saved, s.RobotsSkipped, s.TooManyRequests, s.Errors, s.FinalRate)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "collector:", err)
	os.Exit(1)
}
