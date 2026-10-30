"""How often each rule vetoes true matches and non-matches on a WDC split."""
import collections
import sys

from pricewise_ml import rules, wdc


def report(corner: int, split: str, unseen: int = 0, show: int = 0) -> dict:
    counts = collections.Counter()
    shown = 0
    for r in wdc.pairs(corner, split, unseen):
        a, b = wdc.side(r, "left"), wdc.side(r, "right")
        why = rules.veto(rules.facts(a["title"] or "", a["brand"]), rules.facts(b["title"] or "", b["brand"]))
        counts[(r["label"], why.split(":")[0] if why else None)] += 1
        if why and r["label"] == 1 and shown < show:
            shown += 1
            print("  ", a["title"], "|", b["title"], "->", why)
    pos = sum(n for (l, _), n in counts.items() if l == 1)
    neg = sum(n for (l, _), n in counts.items() if l == 0)
    return {
        "matches": pos, "matches_vetoed": sum(n for (l, k), n in counts.items() if l == 1 and k),
        "non_matches": neg, "non_matches_vetoed": sum(n for (l, k), n in counts.items() if l == 0 and k),
        "by_rule": {f"{'match' if l else 'non-match'} {k}": n for (l, k), n in sorted(counts.items(), key=str) if k},
    }


if __name__ == "__main__":
    print(report(int(sys.argv[1]), sys.argv[2], int(sys.argv[3]) if len(sys.argv) > 3 else 0, show=15))
