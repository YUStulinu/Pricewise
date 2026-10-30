"""Runs the fake-discount detector and the price alerts over the database.

    python -m pricewise_ml.analyse [--db postgres://...]

For every offer it reads the daily series (the offer_daily continuous aggregate), checks each
discount claim and replaces the offer's rows in discount_flags. For every alert it records the days
on which an offer of its product cost no more than the threshold.
"""
from __future__ import annotations

import argparse
import os
import time
from collections import defaultdict

import psycopg

from pricewise_ml.discounts import Day, check

DB = os.environ.get("PRICEWISE_DB", "postgres://pricewise:pricewise@127.0.0.1:5433/pricewise")


def series_by_offer(conn) -> dict[int, list[Day]]:
    out: dict[int, list[Day]] = defaultdict(list)
    rows = conn.execute("SELECT offer_id, day::date, price, reference_price FROM offer_daily ORDER BY offer_id, day")
    for offer, day, price, ref in rows:
        out[offer].append(Day(day, float(price), float(ref) if ref is not None else None))
    return out


def flags(conn) -> dict[str, int]:
    counts: dict[str, int] = defaultdict(int)
    rows = []
    for offer, s in series_by_offer(conn).items():
        for claim in check(s):
            counts["claims"] += 1
            counts["unchecked"] += not claim.checked
            for f in claim.flags:
                counts[f.kind] += 1
                rows.append((offer, f.day, f.kind, f.price, f.reference, f.lowest_30_days, f.explanation))
    with conn.transaction():
        conn.execute("DELETE FROM discount_flags")
        with conn.cursor() as cur:
            cur.executemany(
                "INSERT INTO discount_flags (offer_id, day, kind, price, reference_price, lowest_30_days, explanation) "
                "VALUES (%s, %s, %s, %s, %s, %s, %s)", rows)
    return dict(counts)


def alerts(conn) -> int:
    """Records the offers and days on which a product cost at most an alert's threshold."""
    with conn.transaction():
        cur = conn.execute("""
            INSERT INTO alert_events (alert_id, offer_id, day, price)
            SELECT a.id, o.id, d.day::date, d.price
            FROM alerts a
            JOIN offers o ON o.product_id = a.product_id
            JOIN offer_daily d ON d.offer_id = o.id
            WHERE d.price <= a.threshold AND d.day >= date_trunc('day', a.created_at) - INTERVAL '120 days'
            ON CONFLICT DO NOTHING""")
        return cur.rowcount


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DB)
    args = ap.parse_args()
    t0 = time.time()
    with psycopg.connect(args.db, autocommit=True) as conn:
        conn.execute("CALL refresh_continuous_aggregate('offer_daily', NULL, NULL)")
        counts = flags(conn)
        new = alerts(conn)
    print(f"discount claims {counts.get('claims', 0)}, unchecked {counts.get('unchecked', 0)}; flags "
          f"{ {k: v for k, v in counts.items() if k not in ('claims', 'unchecked')} }; new alert events {new}; "
          f"{time.time() - t0:.1f}s")


if __name__ == "__main__":
    main()
