"""Reads the WDC Products benchmark (Peeters, Der & Bizer, EDBT 2024), downloaded by scripts/fetch-data.sh."""
from __future__ import annotations

import gzip
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
WDC = ROOT / "data" / "raw" / "wdc"


def pairs(corner: int = 80, split: str = "gs", unseen: int = 0, size: str = "large") -> list[dict]:
    """Labelled offer pairs. split is gs (the test set), train or valid; corner is the share of hard
    cases (80, 50 or 20); unseen is the share of test products not in the training set (0, 50, 100)."""
    name = f"wdcproducts{corner}cc{100 - corner}rnd{unseen:03d}un"
    file = WDC / f"{corner}pair" / (f"{name}_gs.json.gz" if split == "gs" else f"{name}_{split}_{size}.json.gz")
    return [json.loads(line) for line in gzip.open(file, "rt", encoding="utf-8")]


def offers(corner: int = 80, split: str = "gs", unseen: int = 0, size: str = "large") -> list[dict]:
    """Single offers with their product cluster (the multi-class version of the benchmark)."""
    name = f"wdcproductsmulti{corner}cc{100 - corner}rnd{unseen:03d}un"
    file = WDC / f"{corner}multi" / (f"{name}_gs.json.gz" if split == "gs" else f"{name}_{split}_{size}.json.gz")
    return [json.loads(line) for line in gzip.open(file, "rt", encoding="utf-8")]


def side(row: dict, s: str) -> dict:
    return {k[: -len(s) - 1]: v for k, v in row.items() if k.endswith("_" + s)}
