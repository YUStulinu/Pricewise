// Package store saves observations in PostgreSQL (with TimescaleDB, see db/schema.sql).
package store

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YUStulinu/Pricewise/collector/internal/crawler"
)

type Store struct {
	pool  *pgxpool.Pool
	mu    sync.Mutex
	shops map[string]int
}

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database %s: %w", cfg.ConnConfig.Host, err)
	}
	return &Store{pool: pool, shops: map[string]int{}}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) shop(ctx context.Context, site crawler.Site) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.shops[site.BaseURL]; ok {
		return id, nil
	}
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO shops (name, base_url, simulated) VALUES ($1, $2, $3)
		ON CONFLICT (base_url) DO UPDATE SET name = EXCLUDED.name, simulated = EXCLUDED.simulated
		RETURNING id`, site.Name, site.BaseURL, site.Simulated).Scan(&id)
	if err != nil {
		return 0, err
	}
	s.shops[site.BaseURL] = id
	return id, nil
}

// Save records an offer (created on first sight) and its price at the time of observation.
func (s *Store) Save(ctx context.Context, o crawler.Observation) error {
	shop, err := s.shop(ctx, o.Site)
	if err != nil {
		return err
	}
	p := o.Product
	nullable := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	var offer int
	err = s.pool.QueryRow(ctx, `
		INSERT INTO offers (shop_id, url, title, brand, sku, gtin, currency, first_seen, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
		ON CONFLICT (url) DO UPDATE SET title = EXCLUDED.title, brand = EXCLUDED.brand, sku = EXCLUDED.sku,
			gtin = EXCLUDED.gtin, currency = EXCLUDED.currency,
			first_seen = LEAST(offers.first_seen, EXCLUDED.first_seen),
			last_seen = GREATEST(offers.last_seen, EXCLUDED.last_seen)
		RETURNING id`,
		shop, o.URL, p.Name, nullable(p.Brand), nullable(p.SKU), nullable(p.GTIN), p.Currency, o.ObservedAt).Scan(&offer)
	if err != nil {
		return err
	}
	var ref any
	if p.ReferencePrice > 0 {
		ref = p.ReferencePrice
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO price_observations (offer_id, observed_at, price, reference_price, available)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (offer_id, observed_at) DO UPDATE SET price = EXCLUDED.price,
			reference_price = EXCLUDED.reference_price, available = EXCLUDED.available`,
		offer, o.ObservedAt, p.Price, ref, p.Available)
	return err
}
