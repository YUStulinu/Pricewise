-- Pricewise schema: shops, the offers seen on them, the products offers are matched to, and the
-- price series (a TimescaleDB hypertable).
CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE shops (
    id          serial PRIMARY KEY,
    name        text NOT NULL,
    base_url    text NOT NULL UNIQUE,
    simulated   boolean NOT NULL DEFAULT false
);

-- A product as Pricewise understands it: a group of offers the matcher decided are the same thing.
CREATE TABLE products (
    id          serial PRIMARY KEY,
    title       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- One product page on one shop.
CREATE TABLE offers (
    id            serial PRIMARY KEY,
    shop_id       integer NOT NULL REFERENCES shops(id),
    url           text NOT NULL UNIQUE,
    title         text NOT NULL,
    brand         text,
    sku           text,
    gtin          text,
    currency      text NOT NULL,
    first_seen    timestamptz NOT NULL,
    last_seen     timestamptz NOT NULL,
    product_id    integer REFERENCES products(id) ON DELETE SET NULL,
    match_score   real,            -- the matcher's confidence that the offer belongs to its product
    match_method  text             -- 'seed', 'model', 'model+llm'
);
CREATE INDEX offers_product ON offers(product_id);

-- What a shop page showed at one time: the price, and the "was" price it crossed out, if any.
CREATE TABLE price_observations (
    offer_id         integer NOT NULL REFERENCES offers(id),
    observed_at      timestamptz NOT NULL,
    price            numeric(12, 2) NOT NULL,
    reference_price  numeric(12, 2),
    available        boolean NOT NULL DEFAULT true,
    PRIMARY KEY (offer_id, observed_at)
);
SELECT create_hypertable('price_observations', by_range('observed_at', INTERVAL '7 days'));

-- The lowest price of each offer per day, kept up to date by TimescaleDB.
CREATE MATERIALIZED VIEW offer_daily WITH (timescaledb.continuous) AS
SELECT offer_id,
       time_bucket(INTERVAL '1 day', observed_at) AS day,
       min(price) AS price,
       max(reference_price) AS reference_price
FROM price_observations
GROUP BY offer_id, time_bucket(INTERVAL '1 day', observed_at)
WITH NO DATA;
SELECT add_continuous_aggregate_policy('offer_daily',
    start_offset => INTERVAL '400 days', end_offset => NULL, schedule_interval => INTERVAL '5 minutes');
ALTER MATERIALIZED VIEW offer_daily SET (timescaledb.materialized_only = false);

-- Discounts whose "was" price the detector does not believe.
CREATE TABLE discount_flags (
    offer_id         integer NOT NULL REFERENCES offers(id),
    day              date NOT NULL,
    kind             text NOT NULL,   -- 'above_30_day_low', 'never_charged', 'inflate_then_drop'
    price            numeric(12, 2) NOT NULL,
    reference_price  numeric(12, 2) NOT NULL,
    lowest_30_days   numeric(12, 2),
    explanation      text NOT NULL,
    PRIMARY KEY (offer_id, day, kind)
);

-- "Tell me when this product costs at most X."
CREATE TABLE alerts (
    id          serial PRIMARY KEY,
    product_id  integer NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    threshold   numeric(12, 2) NOT NULL,
    label       text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE alert_events (
    alert_id     integer NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    offer_id     integer NOT NULL REFERENCES offers(id),
    day          date NOT NULL,
    price        numeric(12, 2) NOT NULL,
    PRIMARY KEY (alert_id, offer_id, day)
);
