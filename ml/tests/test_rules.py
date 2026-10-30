import json
from pathlib import Path

import pytest

from pricewise_ml import rules

CASES = json.loads((Path(__file__).parents[2] / "rules" / "cases.json").read_text(encoding="utf-8"))


@pytest.mark.parametrize("case", CASES, ids=[f"{c['a']} | {c['b']}" for c in CASES])
def test_shared_cases(case):
    why = rules.veto(rules.facts(case["a"], case.get("brand_a")), rules.facts(case["b"], case.get("brand_b")))
    if case["veto"] is None:
        assert why is None, why
    else:
        assert why is not None and why.startswith(case["veto"]), why


def test_units_are_converted():
    assert rules.quantities("2TB and 500 GB") == {"bytes": [2000.0, 500.0]}
    assert rules.quantities("0,5 l") == {"volume": [500.0]}
    assert rules.quantities("Kit 2x8GB") == {"bytes": [8.0, 16.0]}
    assert rules.quantities("ASUS 90MB11B0") == {}


def test_colour_in_a_product_line_is_not_a_colour():
    assert rules.colours("WD Blue SN550") == set()
    assert rules.colours("Case, blue LED") == set()
    assert rules.colours("Case, blue") == {"blue"}


def test_quantities_are_not_codes():
    assert rules.codes("Cruzer 16GB USB 3.0 1080p 100MB/s") == set()
    assert rules.codes("Logitech C310") == {"c310"}
