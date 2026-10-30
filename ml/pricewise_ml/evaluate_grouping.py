"""Evaluates the Rust engine's grouping of the collected offers into products, against the
simulated shops' truth (each listing is an offer of a known WDC product).

Counted over pairs of offers: a pair grouped together is right if both are offers of the same
product. Each product has exactly two offers in two shops, so the shops hold 500 true pairs.

    python -m pricewise_ml.evaluate_grouping [--out results/grouping.json]
"""
from __future__ import annotations

import argparse
import itertools
import json
from collections import defaultdict
from pathlib import Path

import psycopg

from pricewise_ml import wdc
from pricewise_ml.analyse import DB


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--catalogue", default=str(wdc.ROOT / "data" / "sim" / "catalogue.json"))
    ap.add_argument("--out", default=str(wdc.ROOT / "results" / "grouping.json"))
    args = ap.parse_args()
    catalogue = json.loads(Path(args.catalogue).read_text(encoding="utf-8"))
    ports = {i: s["port"] for i, s in enumerate(catalogue["shops"])}
    truth = {f"http://127.0.0.1:{ports[l['shop']]}/p/{l['id']}": l["product"] for l in catalogue["listings"]}

    with psycopg.connect(DB) as conn:
        rows = conn.execute("SELECT url, product_id, match_method FROM offers WHERE product_id IS NOT NULL").fetchall()
    groups = defaultdict(list)
    methods = defaultdict(int)
    for url, product, method in rows:
        groups[product].append(url)
        methods[method] += 1
    predicted = {frozenset(p) for g in groups.values() for p in itertools.combinations(sorted(g), 2)}
    by_product = defaultdict(list)
    for url, _, _ in rows:
        by_product[truth[url]].append(url)
    actual = {frozenset(p) for g in by_product.values() for p in itertools.combinations(sorted(g), 2)}
    tp = len(predicted & actual)
    precision = tp / len(predicted) if predicted else 0.0
    recall = tp / len(actual) if actual else 0.0
    out = {
        "offers": len(rows),
        "products found": len(groups),
        "true products": len(by_product),
        "pairs grouped": len(predicted),
        "true pairs": len(actual),
        "precision": round(precision, 3),
        "recall": round(recall, 3),
        "f1": round(2 * precision * recall / (precision + recall), 3) if tp else 0.0,
        "offers by how they were matched": dict(methods),
    }
    Path(args.out).write_text(json.dumps(out, indent=2))
    print(json.dumps(out, indent=2))


if __name__ == "__main__":
    main()
