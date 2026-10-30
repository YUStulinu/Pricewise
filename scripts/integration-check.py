"""CI: checks a collector run against the simulated shops end to end.

The collector must have stored prices from every open shop, nothing from the shop whose robots.txt
shuts it out, and made no request robots.txt forbids; the detector must flag fake discounts.
"""
import json
import os
import sys
import urllib.request

import psycopg

DB = os.environ.get("PRICEWISE_DB", "postgres://pricewise:pricewise@127.0.0.1:5433/pricewise")
stats = json.load(urllib.request.urlopen(os.environ.get("PRICEWISE_SIM", "http://127.0.0.1:9100") + "/_sim/stats"))
with psycopg.connect(DB) as conn:
    per_shop = dict(conn.execute(
        "SELECT s.name, count(DISTINCT o.id) FROM shops s LEFT JOIN offers o ON o.shop_id = s.id GROUP BY s.name").fetchall())
    observations = conn.execute("SELECT count(*) FROM price_observations").fetchone()[0]
    flags = conn.execute("SELECT count(*) FROM discount_flags").fetchone()[0]
discounts = json.load(open(sys.argv[1]))["summary"]["full detector"]
print(json.dumps({"stats": stats, "offers_per_shop": per_shop, "observations": observations, "flags": flags,
                  "detector": discounts}, indent=1))
problems = []
if per_shop.get("Closed Market", 0) != 0 or stats.get("Closed Market", {}).get("requests", 0) > 1:
    problems.append("the closed shop was crawled")
if any(s["robots_violations"] for s in stats.values()):
    problems.append("requests robots.txt forbids")
if observations < 70 * 100:
    problems.append(f"only {observations} prices stored")
if flags == 0 or discounts["recall"] < 0.75:
    problems.append("the detector caught too little")
if problems:
    sys.exit("; ".join(problems))
