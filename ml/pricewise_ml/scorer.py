"""The match scorer: a logistic regression over the embedding similarity and the rule features.

It is trained on the WDC validation split (the embedder was fine-tuned on the training split, so
the scorer sees similarities the embedder did not fit to) and saved as plain JSON weights that the
Rust engine applies the same way (matcher/src/scorer.rs).
"""
from __future__ import annotations

import json
import math
import re
from pathlib import Path

import numpy as np

from pricewise_ml import rules

FEATURES = ["cosine", "token_jaccard", "code_shared", "quantity_shared", "colour_shared"]


def tokens(title: str) -> set[str]:
    return set(re.findall(r"[a-z0-9]+", rules.fold(title or "")))


def features(cosine: float, a_title: str, b_title: str, a: rules.Facts, b: rules.Facts) -> list[float]:
    ta, tb = tokens(a_title), tokens(b_title)
    jac = len(ta & tb) / len(ta | tb) if ta | tb else 0.0
    agree = rules.agreement(a, b)
    return [cosine, jac, agree["code_shared"], agree["quantity_shared"], agree["colour_shared"]]


class Scorer:
    def __init__(self, weights: list[float], bias: float, threshold: float, borderline: tuple[float, float]):
        self.weights, self.bias, self.threshold, self.borderline = weights, bias, threshold, borderline

    def probability(self, x: list[float]) -> float:
        z = self.bias + sum(w * v for w, v in zip(self.weights, x))
        return 1 / (1 + math.exp(-z))

    def save(self, path: Path) -> None:
        path.write_text(json.dumps({
            "features": FEATURES, "weights": self.weights, "bias": self.bias, "threshold": self.threshold,
            "borderline": list(self.borderline)}, indent=2))

    @staticmethod
    def load(path: Path) -> "Scorer":
        d = json.loads(path.read_text())
        assert d["features"] == FEATURES
        return Scorer(d["weights"], d["bias"], d["threshold"], tuple(d["borderline"]))


def fit(X: np.ndarray, y: np.ndarray) -> tuple[list[float], float]:
    from sklearn.linear_model import LogisticRegression

    m = LogisticRegression(C=10.0, max_iter=2000, class_weight="balanced").fit(X, y)
    return [float(w) for w in m.coef_[0]], float(m.intercept_[0])


def best_threshold(scores: np.ndarray, y: np.ndarray) -> tuple[float, float]:
    """The threshold with the best F1, and that F1."""
    best = (0.5, 0.0)
    for t in np.unique(np.round(scores, 3)):
        pred = scores >= t
        tp = float((pred & (y == 1)).sum())
        if tp == 0:
            continue
        p, r = tp / pred.sum(), tp / (y == 1).sum()
        f = 2 * p * r / (p + r)
        if f > best[1]:
            best = (float(t), f)
    return best
