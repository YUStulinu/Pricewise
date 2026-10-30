"""Builds the simulated shops the demo and the evaluations crawl.

The products and their titles are real: the 500 products of the WDC Products test set whose
products are unseen in training (80% corner cases), two offers each, as two shops wrote them. Only
the shops and the prices are simulated: each offer is placed in one of five shops, and its price is
simulated day by day for 120 days, with discounts written into it whose truth is known:

  genuine            the price drops, and the crossed-out "was" price is the lowest of the last
                     30 days, as the EU Omnibus Directive (Art. 6a of Directive 98/6/EC) requires;
  inflate_then_drop  the price is raised for 1-3 weeks, then "cut" back to about where it was, and
                     the raised price is shown as the "was" price (breaks the 30-day rule);
  long_inflation     the same, but the raised price is held for 5-7 weeks so the "was" price is the
                     30-day low on paper: legal on paper, a sham in substance;
  never_charged      a "was" price the shop never charged;
  unclaimed_drop     the price drops with no "was" price: not a discount claim at all.

A sixth shop forbids Pricewise in its robots.txt; the collector must never read its products.

    python -m pricewise_ml.simulate [--out data/sim/catalogue.json]
"""
from __future__ import annotations

import argparse
import json
from datetime import date, timedelta
from pathlib import Path

import numpy as np

from pricewise_ml import wdc

START = date(2026, 6, 1)
DAYS = 120
# Each shop's own limit on requests a second (it answers 429 above it) and its robots.txt crawl delay.
# Kaufhub's limit is lower than the collector's default rate and not announced anywhere: the
# collector has to notice the 429s and slow down.
SHOPS = [
    {"name": "Voltaro", "port": 9101, "crawl_delay": 0.0, "rate": 60},
    {"name": "Kaufhub", "port": 9102, "crawl_delay": 0.0, "rate": 30},
    {"name": "Megabyte Market", "port": 9103, "crawl_delay": 0.02, "rate": 60},
    {"name": "Tienda Norte", "port": 9104, "crawl_delay": 0.0, "rate": 80},
    {"name": "Nordlys Elektronik", "port": 9105, "crawl_delay": 0.04, "rate": 40},
]
CLOSED_SHOP = {"name": "Closed Market", "port": 9106, "crawl_delay": 0.0, "rate": 60, "disallow_bot": True}
# Euros per unit of each currency, fixed for the simulation.
TO_EUR = {"EUR": 1.0, "USD": 0.92, "GBP": 1.17, "AUD": 0.6, "CAD": 0.67, "CHF": 1.05, "DKK": 0.134, "SEK": 0.087,
          "PLN": 0.23, "RON": 0.2, "NZD": 0.55, "JPY": 0.0062}
SHARES = {"genuine": 0.22, "inflate_then_drop": 0.10, "long_inflation": 0.06, "never_charged": 0.06, "unclaimed_drop": 0.10}


def base_price(offer: dict, rng: np.random.Generator) -> float:
    try:
        p = float(offer["price"]) * TO_EUR[offer["priceCurrency"]]
        if 1 <= p <= 20000:
            return p
    except (TypeError, ValueError, KeyError):
        pass
    return float(np.exp(rng.normal(4.3, 0.9)))


def walk(start: float, rng: np.random.Generator) -> np.ndarray:
    """A shop's everyday price: flat, with an occasional small change."""
    prices = np.empty(DAYS)
    p = start
    for d in range(DAYS):
        if rng.random() < 0.03:
            p *= 1 + rng.choice([-1, 1]) * rng.uniform(0.02, 0.06)
        prices[d] = p
    return prices


def charm(p: float) -> float:
    """Shop prices end in .99 or .49."""
    return max(0.49, np.floor(p) + (0.99 if p - np.floor(p) > 0.5 else 0.49))


def inject(kind: str, prices: np.ndarray, rng: np.random.Generator) -> tuple[np.ndarray, np.ndarray, dict]:
    """Writes one discount event of this kind into a price series."""
    prices = prices.copy()
    ref = np.full(DAYS, np.nan)
    sale_len = int(rng.integers(5, 15))
    if kind == "long_inflation":
        rise_len = int(rng.integers(35, 50))
    elif kind == "inflate_then_drop":
        rise_len = int(rng.integers(7, 22))
    else:
        rise_len = 0
    start = int(rng.integers(max(35, rise_len + 5), DAYS - sale_len - 1))
    end = start + sale_len
    normal = prices[start - 1 - rise_len]
    if kind == "genuine":
        prices[start:end] = prices[start - 1] * (1 - rng.uniform(0.1, 0.3))
        ref[start:end] = prices[start - 30:start].min()
    elif kind in ("inflate_then_drop", "long_inflation"):
        high = normal * (1 + rng.uniform(0.15, 0.4))
        prices[start - rise_len:start] = high
        prices[start:end] = normal * (1 + rng.uniform(-0.03, 0.03))
        ref[start:end] = high
    elif kind == "never_charged":
        prices[start:end] = prices[start - 1] * (1 - rng.uniform(0.0, 0.05))
        ref[start:end] = prices[start - 30:end].max() * (1 + rng.uniform(0.3, 0.6))
    elif kind == "unclaimed_drop":
        prices[start:end] = prices[start - 1] * (1 - rng.uniform(0.1, 0.25))
    event = {"kind": kind, "fake": kind in ("inflate_then_drop", "long_inflation", "never_charged"),
             "start": (START + timedelta(start)).isoformat(), "end": (START + timedelta(end - 1)).isoformat(),
             "raised_from": (START + timedelta(start - rise_len)).isoformat() if rise_len else None}
    return prices, ref, event


def build(seed: int = 20) -> dict:
    rng = np.random.default_rng(seed)
    offers = wdc.offers(80, "gs", 100)
    by_product: dict[int, list[dict]] = {}
    for o in offers:
        by_product.setdefault(o["cluster_id"], []).append(o)
    kinds = list(SHARES) + ["none"]
    weights = list(SHARES.values()) + [1 - sum(SHARES.values())]
    listings = []
    for product, group in sorted(by_product.items()):
        base = base_price(next((o for o in group if o["price"]), group[0]), rng)
        shops = rng.choice(len(SHOPS), size=len(group), replace=False)
        for o, shop in zip(group, shops):
            prices = walk(base * rng.uniform(0.93, 1.08), rng)
            kind = str(rng.choice(kinds, p=weights))
            ref = np.full(DAYS, np.nan)
            events = []
            if kind != "none":
                prices, ref, event = inject(kind, prices, rng)
                events.append(event)
            listings.append(_listing(o, int(shop), product, prices, ref, events))
    # The closed shop lists a sample of the same products; nothing of it may ever be collected.
    for o in rng.choice(offers, size=40, replace=False):
        listings.append(_listing(o, len(SHOPS), o["cluster_id"], walk(base_price(o, rng), rng), np.full(DAYS, np.nan), []))
    return {
        "note": "Simulated shops. Product titles are real offers from the WDC Products benchmark; shops and prices are simulated.",
        "start": START.isoformat(), "days": DAYS, "currency": "EUR",
        "shops": SHOPS + [CLOSED_SHOP],
        "listings": listings,
    }


def build_fixture(seed: int = 7, products: int = 40) -> dict:
    """A small catalogue with made-up titles, for tests that cannot download WDC Products."""
    rng = np.random.default_rng(seed)
    kinds = list(SHARES) + ["none"]
    weights = list(SHARES.values()) + [1 - sum(SHARES.values())]
    listings = []
    for product in range(products):
        base = float(np.exp(rng.normal(4.0, 0.6)))
        for n, shop in enumerate(rng.choice(len(SHOPS), size=2, replace=False)):
            prices = walk(base * rng.uniform(0.93, 1.08), rng)
            kind = str(rng.choice(kinds, p=weights))
            ref = np.full(DAYS, np.nan)
            events = []
            if kind != "none":
                prices, ref, event = inject(kind, prices, rng)
                events.append(event)
            o = {"id": product * 10 + n, "title": f"Testbrand Gadget {product} {'Pro' if n else 'Pro edition'} {16 * (1 + product % 4)}GB",
                 "brand": "Testbrand", "description": "A made-up product for tests."}
            listings.append(_listing(o, int(shop), product, prices, ref, events))
    return {"note": "Test fixture: made-up products and simulated prices.", "start": START.isoformat(), "days": DAYS,
            "currency": "EUR", "shops": SHOPS + [CLOSED_SHOP], "listings": listings}


def _listing(o: dict, shop: int, product: int, prices: np.ndarray, ref: np.ndarray, events: list) -> dict:
    return {
        "id": f"{shop}-{o['id']}", "shop": shop, "product": int(product), "title": o["title"],
        "brand": o["brand"] if isinstance(o["brand"], str) and len(o["brand"]) < 40 else None,
        "description": (o["description"] or "")[:400],
        "prices": [charm(p) for p in prices],
        "reference": [None if np.isnan(r) else charm(r) for r in ref],
        "events": events,
    }


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=str(wdc.ROOT / "data" / "sim" / "catalogue.json"))
    ap.add_argument("--seed", type=int, default=20)
    ap.add_argument("--fixture", action="store_true", help="made-up products instead of WDC Products (for CI)")
    args = ap.parse_args()
    catalogue = build_fixture() if args.fixture else build(args.seed)
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(catalogue, ensure_ascii=False), encoding="utf-8")
    counts: dict[str, int] = {}
    for item in catalogue["listings"]:
        for e in item["events"]:
            counts[e["kind"]] = counts.get(e["kind"], 0) + 1
    print(f"{len(catalogue['listings'])} listings in {len(catalogue['shops'])} shops -> {out}; events {counts}")


if __name__ == "__main__":
    main()
