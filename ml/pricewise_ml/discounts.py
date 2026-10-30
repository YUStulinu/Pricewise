"""The fake-discount detector.

A shop announces a discount by showing a crossed-out "was" price. Since 28 May 2022 the EU Omnibus
Directive (Art. 6a of Directive 98/6/EC, as amended by Directive (EU) 2019/2161) requires that
"was" price to be the lowest price the shop charged in the 30 days before the reduction. The
detector checks each announced discount against the prices Pricewise itself observed:

  above_30_day_low   the "was" price is higher than the lowest price observed in the 30 days
                     before the discount began;
  never_charged      the "was" price is higher than any price observed in the 90 days before;
  inflate_then_drop  the price was pushed up (by 10% or more) in the weeks before the discount,
                     and the "discount" only brings it back to where it was before the rise. This
                     catches the shop that holds the raised price for more than 30 days, so that
                     the "was" price is the 30-day low on paper.

A check is only made when the history covers the period it needs; otherwise the discount is
reported as unchecked rather than trusted or flagged.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import date, timedelta
from statistics import median

TOLERANCE = 0.01  # prices are rounded to .49/.99, so allow 1%
RISE = 0.10  # a rise of 10% or more counts as inflating the price
MIN_COVERAGE = 0.6  # share of a window's days that must have been observed


@dataclass
class Day:
    day: date
    price: float
    reference: float | None  # the crossed-out "was" price, if the shop showed one


@dataclass
class Flag:
    day: date  # the first day of the discount
    kind: str
    price: float
    reference: float
    lowest_30_days: float | None
    explanation: str


@dataclass
class Claim:
    start: date
    end: date
    price: float
    reference: float
    flags: list[Flag]
    checked: bool


def claims(series: list[Day], gap: int = 2) -> list[tuple[int, int]]:
    """Runs of days showing a "was" price, as (first, last) indices; gaps of up to `gap` missing days
    (pages that could not be read) do not end a run."""
    runs = []
    start = None
    for i, d in enumerate(series):
        if d.reference is None:
            continue
        if start is not None and (d.day - series[last].day).days <= gap + 1:
            last = i
            continue
        if start is not None:
            runs.append((start, last))
        start = last = i
    if start is not None:
        runs.append((start, last))
    return runs


def window(series: list[Day], start: date, days: int) -> list[Day]:
    """The observations in the `days` days before `start`."""
    lo = start - timedelta(days)
    return [d for d in series if lo <= d.day < start]


def covered(obs: list[Day], days: int) -> bool:
    return len(obs) >= MIN_COVERAGE * days


def check(series: list[Day]) -> list[Claim]:
    """Every discount claim in an offer's daily series (sorted by day), with its flags."""
    out = []
    for i, j in claims(series):
        first = series[i]
        start, price, ref = first.day, first.price, first.reference
        flags: list[Flag] = []
        last30 = window(series, start, 30)
        checked = covered(last30, 30)
        low30 = min(d.price for d in last30) if last30 else None
        if checked and ref > low30 * (1 + TOLERANCE):
            last90 = window(series, start, 90)
            high = max(d.price for d in last90)
            if ref > high * (1 + TOLERANCE):
                flags.append(Flag(start, "never_charged", price, ref, low30,
                                  f"The 'was' price {ref:.2f} was never charged: the highest price in the "
                                  f"{len(last90)} days observed before was {high:.2f}."))
            flags.append(Flag(start, "above_30_day_low", price, ref, low30,
                              f"The 'was' price {ref:.2f} is above the lowest price of the 30 days before the "
                              f"discount, {low30:.2f}; EU law requires the 30-day lowest."))
        rise = inflation(series, i)
        if rise:
            checked = True
            raised_on, before = rise
            if price >= before * (1 - 0.03):
                flags.append(Flag(start, "inflate_then_drop", price, ref, low30,
                                  f"The price was raised on {raised_on:%d %b} from about {before:.2f}; the "
                                  f"'discount' to {price:.2f} only brings it back to that."))
        out.append(Claim(start, series[j].day, price, ref, flags, checked))
    return out


def inflation(series: list[Day], i: int, lookback: int = 75) -> tuple[date, float] | None:
    """The last rise of RISE or more in the `lookback` days before series[i] that the price held
    until the discount, and the typical price in the three weeks before that rise."""
    start = series[i].day
    k = i - 1
    # Walk back over the raised plateau: days whose price stays near the price just before the discount.
    if k < 0:
        return None
    plateau = series[k].price
    while k > 0 and series[k - 1].price >= plateau * (1 - TOLERANCE) and (start - series[k - 1].day).days <= lookback:
        k -= 1
    if k == 0 or (start - series[k].day).days > lookback:
        return None
    before = window(series, series[k].day, 21)
    if not covered(before, 21):
        return None
    typical = median(d.price for d in before)
    if plateau < typical * (1 + RISE):
        return None
    return series[k].day, typical
