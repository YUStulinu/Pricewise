"""Evaluates product matching on the WDC Products test sets (80% corner cases).

Each method decides "same product or not" for every labelled pair; thresholds and the scorer are
fitted on the validation split only, never on a test set. The test sets are the benchmark's three:
products seen in training, half unseen, all unseen.

    python -m pricewise_ml.evaluate_matching [--llm http://127.0.0.1:8094]

writes results/matching.json and models/scorer.json.
"""
from __future__ import annotations

import argparse
import json
import time

import numpy as np

from pricewise_ml import embed, rules, scorer, wdc

RESULTS = wdc.ROOT / "results"


def metrics(pred: np.ndarray, y: np.ndarray) -> dict:
    tp = int((pred & (y == 1)).sum())
    fp = int((pred & (y == 0)).sum())
    fn = int((~pred & (y == 1)).sum())
    p = tp / (tp + fp) if tp + fp else 0.0
    r = tp / (tp + fn) if tp + fn else 0.0
    return {"precision": round(p, 3), "recall": round(r, 3), "f1": round(2 * p * r / (p + r), 3) if p + r else 0.0,
            "tp": tp, "fp": fp, "fn": fn}


class Split:
    """One labelled split with everything the methods need."""

    def __init__(self, rows: list[dict]):
        self.rows = rows
        self.y = np.array([r["label"] for r in rows])
        self.a = [r["title_left"] or "" for r in rows]
        self.b = [r["title_right"] or "" for r in rows]
        self.fa = [rules.facts(r["title_left"] or "", r["brand_left"]) for r in rows]
        self.fb = [rules.facts(r["title_right"] or "", r["brand_right"]) for r in rows]
        self.veto = np.array([rules.veto(x, z) is not None for x, z in zip(self.fa, self.fb)])

    def cosines(self, model) -> np.ndarray:
        tok, emb, dev = model
        ea, eb = embed.encode(tok, emb, dev, self.a), embed.encode(tok, emb, dev, self.b)
        return (ea * eb).sum(1)

    def features(self, cos: np.ndarray) -> np.ndarray:
        return np.array([scorer.features(float(c), x, z, fx, fz)
                         for c, x, z, fx, fz in zip(cos, self.a, self.b, self.fa, self.fb)])


def tfidf_cosines(train: list[str], a: list[str], b: list[str]) -> np.ndarray:
    from sklearn.feature_extraction.text import TfidfVectorizer

    vec = TfidfVectorizer(analyzer="char_wb", ngram_range=(3, 5), sublinear_tf=True).fit(train)
    va, vb = vec.transform(a), vec.transform(b)
    return np.asarray(va.multiply(vb).sum(1)).ravel()


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--llm", help="llama-server address; asks it about the scorer's borderline pairs")
    args = ap.parse_args()
    t0 = time.time()

    valid = Split(wdc.pairs(80, "valid"))
    tests = {f"{u}% unseen": Split(wdc.pairs(80, "gs", u)) for u in (0, 50, 100)}
    corpus = [r["title_left"] or "" for r in wdc.pairs(80, "train")] + [r["title_right"] or "" for r in wdc.pairs(80, "train")]

    base = embed.load()
    tuned = embed.load(embed.OUT)
    cos = {"base": valid.cosines(base), "tuned": valid.cosines(tuned), "tfidf": tfidf_cosines(corpus, valid.a, valid.b)}
    test_cos = {name: {"base": s.cosines(base), "tuned": s.cosines(tuned), "tfidf": tfidf_cosines(corpus, s.a, s.b)}
                for name, s in tests.items()}

    # Thresholds on the validation split.
    thresholds = {k: scorer.best_threshold(v, valid.y)[0] for k, v in cos.items()}
    thresholds["tuned+rules"] = scorer.best_threshold(np.where(valid.veto, -1, cos["tuned"]), valid.y)[0]
    Xv = valid.features(cos["tuned"])
    w, b = scorer.fit(Xv, valid.y)
    sc = scorer.Scorer(w, b, 0.5, (0.0, 0.0))
    pv = np.array([sc.probability(list(x)) for x in Xv])
    pv = np.where(valid.veto, 0.0, pv)
    sc.threshold = scorer.best_threshold(pv, valid.y)[0]
    # Borderline: the band around the threshold holding about 8% of the validation pairs.
    band = np.quantile(np.abs(pv - sc.threshold)[~valid.veto], 0.08)
    sc.borderline = (round(max(sc.threshold - band, 0.0), 3), round(min(sc.threshold + band, 1.0), 3))
    (wdc.ROOT / "models").mkdir(exist_ok=True)
    sc.save(wdc.ROOT / "models" / "scorer.json")
    print(f"thresholds {thresholds}; scorer {dict(zip(scorer.FEATURES, np.round(w, 2)))} bias {b:.2f} "
          f"threshold {sc.threshold} borderline {sc.borderline}", flush=True)

    judge = None
    if args.llm:
        from pricewise_ml.llm_judge import Judge
        judge = Judge(args.llm)

    results = {}
    for name, s in tests.items():
        c = test_cos[name]
        X = s.features(c["tuned"])
        p = np.where(s.veto, 0.0, np.array([sc.probability(list(x)) for x in X]))
        r = {
            "TF-IDF character n-grams (text only)": metrics(c["tfidf"] >= thresholds["tfidf"], s.y),
            "multilingual-e5-small, not fine-tuned": metrics(c["base"] >= thresholds["base"], s.y),
            "fine-tuned embedder": metrics(c["tuned"] >= thresholds["tuned"], s.y),
            "fine-tuned embedder + rule vetoes": metrics((c["tuned"] >= thresholds["tuned+rules"]) & ~s.veto, s.y),
            "scorer + rule vetoes": metrics(p >= sc.threshold, s.y),
            "rule vetoes alone, matches wrongly vetoed": int((s.veto & (s.y == 1)).sum()),
            "rule vetoes alone, non-matches vetoed": int((s.veto & (s.y == 0)).sum()),
        }
        if judge:
            lo, hi = sc.borderline
            ask = np.where((p >= lo) & (p <= hi) & ~s.veto)[0]
            pred = p >= sc.threshold
            answers = judge.many([(s.a[i], s.b[i]) for i in ask])
            for i, same in zip(ask, answers):
                if same is not None:
                    pred[i] = same
            r["scorer + rule vetoes + LLM on borderline pairs"] = metrics(pred, s.y)
            r["borderline pairs sent to the LLM"] = len(ask)
            r["LLM accuracy on them"] = round(float(np.mean([a == bool(s.y[i]) for i, a in zip(ask, answers) if a is not None])), 3)
        results[name] = r
        print(name, json.dumps(r, indent=1), flush=True)

    out = {"benchmark": "WDC Products, 80% corner cases, test sets (4,500 pairs each, 500 matches)",
           "thresholds_from": "the validation split (4,500 pairs)", "thresholds": thresholds,
           "scorer": json.loads((wdc.ROOT / "models" / "scorer.json").read_text()),
           "results": results, "seconds": round(time.time() - t0)}
    RESULTS.mkdir(exist_ok=True)
    (RESULTS / ("matching.json" if judge else "matching-no-llm.json")).write_text(json.dumps(out, indent=2))


if __name__ == "__main__":
    main()
