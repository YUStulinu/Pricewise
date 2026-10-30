"""Evaluates the fake-discount detector on the prices the collector gathered from the simulated
shops, against the discounts the simulation wrote in (data/sim/catalogue.json).

The detector only sees what the collector stored (including the days it could not read a page);
the truth comes from the simulation. A discount event counts as caught if the detector flags the
offer's claim that starts inside the event. Two detectors are compared: the Omnibus 30-day rule
alone, and the full detector with the inflation pattern.

    python -m pricewise_ml.evaluate_discounts

writes results/discounts.json.
"""
from __future__ import annotations

import argparse
import json
from collections import defaultdict
from datetime import date
from pathlib import Path

import psycopg

from pricewise_ml import wdc
from pricewise_ml.analyse import DB, series_by_offer
from pricewise_ml.discounts import check

OMNIBUS = {"above_30_day_low", "never_charged"}


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--catalogue", default=str(wdc.ROOT / "data" / "sim" / "catalogue.json"))
    ap.add_argument("--out", default=str(wdc.ROOT / "results" / "discounts.json"))
    args = ap.parse_args()
    catalogue = json.loads(Path(args.catalogue).read_text(encoding="utf-8"))
    ports = {i: s["port"] for i, s in enumerate(catalogue["shops"])}
    truth = {f"http://127.0.0.1:{ports[l['shop']]}/p/{l['id']}": l["events"] for l in catalogue["listings"]}

    with psycopg.connect(DB, autocommit=True) as conn:
        conn.execute("CALL refresh_continuous_aggregate('offer_daily', NULL, NULL)")
        urls = dict(conn.execute("SELECT id, url FROM offers").fetchall())
        series = series_by_offer(conn)

    tally = defaultdict(lambda: defaultdict(int))
    spurious = defaultdict(int)
    examples = defaultdict(list)
    for offer, s in series.items():
        events = [e for e in truth.get(urls[offer], []) if e["kind"] != "unclaimed_drop"]
        claims = check(s)
        matched = set()
        for e in events:
            start, end = date.fromisoformat(e["start"]), date.fromisoformat(e["end"])
            claim = next((c for c in claims if start <= c.start <= end), None)
            t = tally[e["kind"]]
            t["events"] += 1
            if claim is None:
                t["not_seen"] += 1  # e.g. every day of it fell on pages that could not be read
                continue
            matched.add(id(claim))
            kinds = {f.kind for f in claim.flags}
            t["unchecked"] += not claim.checked
            t["flagged_by_omnibus_rule"] += bool(kinds & OMNIBUS)
            t["flagged_by_full_detector"] += bool(kinds)
            for k in kinds:
                t[f"flag {k}"] += 1
            if e["fake"] != bool(kinds) and len(examples[e["kind"]]) < 3:
                examples[e["kind"]].append({"url": urls[offer], "event": e, "flags": sorted(kinds)})
        for c in claims:
            if id(c) not in matched:
                spurious["claims"] += 1
                spurious["flagged"] += bool(c.flags)

    def rate(kind, key):
        t = tally[kind]
        seen = t["events"] - t["not_seen"]
        return round(t[key] / seen, 3) if seen else None

    fakes = ["inflate_then_drop", "long_inflation", "never_charged"]
    summary = {}
    for detector, key in (("Omnibus 30-day rule alone", "flagged_by_omnibus_rule"), ("full detector", "flagged_by_full_detector")):
        caught = sum(tally[k][key] for k in fakes)
        fake_seen = sum(tally[k]["events"] - tally[k]["not_seen"] for k in fakes)
        false_alarms = tally["genuine"][key]
        flagged = caught + false_alarms
        summary[detector] = {
            "fake discounts caught": f"{caught}/{fake_seen}",
            "recall": round(caught / fake_seen, 3),
            "by kind": {k: rate(k, key) for k in fakes},
            "genuine discounts flagged (false alarms)": f"{false_alarms}/{tally['genuine']['events'] - tally['genuine']['not_seen']}",
            "precision": round(caught / flagged, 3) if flagged else None,
        }
    out = {
        "data": "prices collected by the Go collector from the simulated shops over 120 days; truth from the simulation",
        "summary": summary,
        "events": {k: dict(v) for k, v in tally.items()},
        "claims not matching any simulated event": dict(spurious),
        "missed or wrong, examples": examples,
    }
    Path(args.out).parent.mkdir(parents=True, exist_ok=True)
    Path(args.out).write_text(json.dumps(out, indent=2, default=str))
    print(json.dumps(summary, indent=2))
    print(json.dumps(out["events"], indent=1))


if __name__ == "__main__":
    main()
