"""Checks that the Rust engine decides like the Python implementation it was ported from.

Runs `pricewise-match pairs` (ONNX embeddings, Rust rules and scorer) and the Python pipeline
(PyTorch embeddings, Python rules and scorer) on the WDC test pairs, and compares their
similarities and decisions. Also runs `pricewise-match bench` for the HNSW index.

    python -m pricewise_ml.compare_engines [--engine matcher/target/release/pricewise-match]

writes results/engines.json.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import numpy as np

from pricewise_ml import embed, rules, scorer, wdc
from pricewise_ml.evaluate_matching import Split, metrics


def main() -> None:
    exe = "pricewise-match.exe" if sys.platform == "win32" else "pricewise-match"
    ap = argparse.ArgumentParser()
    ap.add_argument("--engine", default=str(wdc.ROOT / "matcher" / "target" / "release" / exe))
    args = ap.parse_args()
    sc = scorer.Scorer.load(wdc.ROOT / "models" / "scorer.json")
    tuned = embed.load(embed.OUT)
    out = {}
    tmp = Path(tempfile.mkdtemp())
    for unseen in (0, 100):
        rows = wdc.pairs(80, "gs", unseen)
        s = Split(rows)
        src, dst = tmp / f"pairs{unseen}.jsonl", tmp / f"out{unseen}.jsonl"
        with src.open("w", encoding="utf-8") as f:
            for r in rows:
                f.write(json.dumps({"a": r["title_left"] or "", "b": r["title_right"] or "",
                                    "brand_a": r["brand_left"], "brand_b": r["brand_right"]}, ensure_ascii=False) + "\n")
        t0 = time.time()
        subprocess.run([args.engine, "--model", str(embed.OUT), "--scorer", str(wdc.ROOT / "models" / "scorer.json"),
                        "pairs", "--input", str(src), "--output", str(dst)], check=True)
        rust_seconds = time.time() - t0
        rust = [json.loads(line) for line in dst.read_text(encoding="utf-8").splitlines()]
        cos = s.cosines(tuned)
        X = s.features(cos)
        p = np.where(s.veto, 0.0, np.array([sc.probability(list(x)) for x in X]))
        py_same = p >= sc.threshold
        rust_same = np.array([r["same"] for r in rust])
        rust_veto = np.array([r["veto"] is not None for r in rust])
        out[f"{unseen}% unseen"] = {
            "pairs": len(rows),
            "max cosine difference (ONNX in Rust vs PyTorch)": float(np.max(np.abs(cos - np.array([r["cosine"] for r in rust])))),
            "same veto decision": float(np.mean(rust_veto == s.veto)),
            "same match decision": float(np.mean(rust_same == py_same)),
            "Rust engine": metrics(rust_same, s.y),
            "Python": metrics(py_same, s.y),
            "Rust seconds for all pairs (CPU, embeddings included)": round(rust_seconds, 1),
        }
        print(unseen, json.dumps(out[f"{unseen}% unseen"], indent=1), flush=True)

    titles = tmp / "titles.txt"
    offers = wdc.offers(80, "train") + wdc.offers(80, "valid") + wdc.offers(80, "gs", 0) + wdc.offers(80, "gs", 100)
    titles.write_text("\n".join(sorted({" ".join((o["title"] or "").split()) for o in offers} - {""})), encoding="utf-8")
    bench = subprocess.run([args.engine, "--model", str(embed.OUT), "bench", "--input", str(titles)],
                           check=True, capture_output=True, text=True)
    out["hnsw"] = json.loads(bench.stdout)
    print(json.dumps(out["hnsw"], indent=1))
    (wdc.ROOT / "results" / "engines.json").write_text(json.dumps(out, indent=2))


if __name__ == "__main__":
    main()
