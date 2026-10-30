from datetime import date, timedelta

from pricewise_ml.discounts import Day, check

START = date(2026, 6, 1)


def series(prices, refs=None):
    refs = refs or {}
    return [Day(START + timedelta(i), p, refs.get(i)) for i, p in enumerate(prices) if p is not None]


def kinds(s):
    return [sorted(f.kind for f in c.flags) for c in check(s)]


def test_genuine_discount_passes():
    prices = [100.0] * 50 + [79.0] * 10
    refs = {i: 100.0 for i in range(50, 60)}
    assert kinds(series(prices, refs)) == [[]]


def test_short_inflation_breaks_the_30_day_rule():
    prices = [100.0] * 40 + [129.0] * 14 + [99.0] * 10
    refs = {i: 129.0 for i in range(54, 64)}
    assert kinds(series(prices, refs)) == [["above_30_day_low", "inflate_then_drop"]]


def test_long_inflation_is_legal_on_paper_but_caught():
    prices = [100.0] * 30 + [130.0] * 40 + [100.0] * 10
    refs = {i: 130.0 for i in range(70, 80)}
    assert kinds(series(prices, refs)) == [["inflate_then_drop"]]


def test_never_charged():
    prices = [100.0] * 60
    refs = {i: 149.0 for i in range(45, 55)}
    assert kinds(series(prices, refs)) == [["above_30_day_low", "never_charged"]]


def test_missing_days_do_not_split_a_discount_or_hide_it():
    prices = [100.0] * 40 + [129.0] * 14 + [99.0, None, 99.0, 99.0]
    prices = [None if i % 9 == 4 else p for i, p in enumerate(prices)]  # some days could not be read
    refs = {i: 129.0 for i in range(54, 58)}
    out = check(series(prices, refs))
    assert len(out) == 1 and "above_30_day_low" in [f.kind for f in out[0].flags]


def test_too_little_history_is_unchecked_not_flagged():
    prices = [100.0] * 10 + [80.0] * 5
    refs = {i: 140.0 for i in range(10, 15)}
    [claim] = check(series(prices, refs))
    assert claim.flags == [] and not claim.checked


def test_a_small_price_step_is_not_inflation():
    prices = [100.0] * 30 + [104.0] * 40 + [100.0] * 10
    refs = {i: 104.0 for i in range(70, 80)}
    assert kinds(series(prices, refs)) == [[]]
