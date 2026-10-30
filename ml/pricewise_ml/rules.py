"""Normalisation rules for product titles, used as hard vetoes on matches.

Embeddings are good at "these are both 1 TB WD SSDs" and bad at "SN550 is not SN750" or "16 GB is
not 64 GB". These rules read the facts that decide identity out of a title (capacities, colours,
model codes, brand) and veto a pair whose facts contradict each other. A rule only vetoes when both
titles state the fact: a title that says nothing about colour is compatible with every colour.

The Rust engine (matcher/src/rules.rs) implements the same rules with the same regular expressions
(no look-arounds, so both regex engines agree); both are tested against rules/cases.json.
"""
from __future__ import annotations

import re
import unicodedata
from dataclasses import dataclass, field

# Units of each quantity, as multipliers of a base unit ("go" is the French gigaoctet).
UNITS: dict[str, tuple[str, float]] = {
    "tb": ("bytes", 1000.0), "gb": ("bytes", 1.0), "go": ("bytes", 1.0), "mb": ("bytes", 0.001),
    "l": ("volume", 1000.0), "ml": ("volume", 1.0), "cl": ("volume", 10.0),
    "kg": ("mass", 1000.0), "g": ("mass", 1.0),
    "mah": ("charge", 1.0),
    "w": ("power", 1.0), "kw": ("power", 1000.0),
    # Memory speeds; GHz is left out because shops quote a processor's base or boost clock.
    "mhz": ("frequency", 1.0),
}
# An optional kit count ("2x8GB"), a number and a unit. Boundaries are checked by hand.
QUANTITY = re.compile(r"(\d+ ?x ?)?(\d+(?:[.,]\d+)?) ?(tb|gb|go|mb|mhz|ml|cl|kg|mah|kw|l|g|w)")
# Two values of a quantity are the same within 5%: 8192 MB is 8 GB, 1 TB is 1024 GB.
TOLERANCE = 0.05

# Colour words in the languages WDC titles come in (accents folded), mapped to one name. Words that
# are also common words elsewhere ("or", "vit", "sort", Danish "rod") are left out.
COLOURS: dict[str, str] = {}
for canonical, words in {
    "black": "black schwarz noir negro nero zwart svart czarny negru preto musta",
    "white": "white weiss blanc blanco blanca bianco wit hvid bialy alb branco valkoinen",
    "red": "red rot rouge rojo roja rosso rood czerwony rosu vermelho punainen",
    "blue": "blue blau bleu azul blu blauw niebieski albastru sininen",
    "green": "green grun vert verde groen zielony",
    "grey": "grey gray grau gris grigio grijs szary gri cinza harmaa",
    "silver": "silver silber argent plata argento zilver srebrny argintiu prata hopea",
    "gold": "gold oro goud guld zloty auriu dourado kulta",
    "pink": "pink rosa roze rozowy roz",
    "yellow": "yellow gelb jaune amarillo giallo geel gul zolty galben amarelo",
    "purple": "purple lila violet violeta viola paars fioletowy mov roxo",
    "orange": "orange naranja arancione oranje pomaranczowy portocaliu laranja",
    "brown": "brown braun marron marrone bruin brun brazowy maro castanho",
}.items():
    for w in words.split():
        COLOURS[w] = canonical
# A colour word after a product-line brand ("WD Blue") or before a part ("red LED") is not the
# product's colour.
LINE_BRANDS = {"wd"}
COLOURED_PARTS = {"led", "leds", "dot", "light", "laser"}

# Brands with more than one name; the keys are letters and digits only.
BRAND_ALIASES = {
    "wd": "westerndigital", "hgst": "westerndigital",
    "hp": "hewlettpackard", "hpe": "hewlettpackard", "hewlettpackardenterprise": "hewlettpackard",
    "lgelectronics": "lg", "samsungelectronics": "samsung", "kingstontechnology": "kingston",
    "hyperx": "kingston", "crucialtechnology": "crucial", "micron": "crucial", "appleinc": "apple",
    "logi": "logitech", "adata": "adata", "xpg": "adata", "raybansunglasses": "rayban",
}
NAMES: dict[str, set[str]] = {}
for alias, canonical in BRAND_ALIASES.items():
    NAMES.setdefault(canonical, {canonical}).add(alias)

# Tokens for model codes; "/" separates alternatives ("C831/841").
TOKEN = re.compile(r"[a-z0-9]+(?:[-.][a-z0-9]+)*")
# Numbers with a unit or suffix are measurements, not model codes.
MEASURE = re.compile(r"\d+(?:[.,]\d+)?(?:tb|gb|go|mb|mbs|mbps|gbps|gbs|mhz|ghz|hz|ml|cl|l|kg|g|mah|kw|w|v|mm|cm|m|in|p|k|x|st|nd|rd|th|tpi|c)")


def fold(text: str) -> str:
    """Lower case without accents ("Blanc" and "blánc" are the same word)."""
    text = unicodedata.normalize("NFKD", text.lower())
    text = "".join(c for c in text if not unicodedata.combining(c))
    for a, b in (("ß", "ss"), ("ø", "o"), ("æ", "ae"), ("ł", "l"), ("đ", "d")):
        text = text.replace(a, b)
    return text


def key(text: str) -> str:
    """Letters and digits only: "Ray-Ban" and "rayban" have the same key."""
    return re.sub(r"[^a-z0-9]", "", fold(text))


@dataclass
class Facts:
    quantities: dict[str, list[float]] = field(default_factory=dict)
    colours: set[str] = field(default_factory=set)
    codes: set[str] = field(default_factory=set)
    brand: str | None = None
    title_key: str = ""


def quantities(title: str) -> dict[str, list[float]]:
    text = fold(title)
    out: dict[str, list[float]] = {}
    for m in QUANTITY.finditer(text):
        before = text[m.start() - 1] if m.start() > 0 else " "
        after = text[m.end()] if m.end() < len(text) else " "
        if before.isalnum() or before in ".," or after.isalnum():
            continue
        kind, scale = UNITS[m.group(3)]
        value = float(m.group(2).replace(",", ".")) * scale
        values = out.setdefault(kind, [])
        values.append(value)
        if m.group(1):
            values.append(value * int(m.group(1).rstrip(" x")))
    return out


def colours(title: str) -> set[str]:
    words = re.findall(r"[a-z]+", fold(title))
    found = set()
    for i, w in enumerate(words):
        if w not in COLOURS:
            continue
        if (i > 0 and words[i - 1] in LINE_BRANDS) or (i + 1 < len(words) and words[i + 1] in COLOURED_PARTS):
            continue
        found.add(COLOURS[w])
    return found


def is_code(token: str) -> bool:
    if len(token) < 3 or not any(c.isalpha() for c in token) or not any(c.isdigit() for c in token):
        return False
    return MEASURE.fullmatch(token) is None


def codes(title: str) -> set[str]:
    return {t.replace("-", "").replace(".", "") for t in TOKEN.findall(fold(title)) if is_code(t)}


def brand(name: str | None) -> str | None:
    k = key(name or "")
    return BRAND_ALIASES.get(k, k) or None


def facts(title: str, brand_name: str | None = None) -> Facts:
    return Facts(quantities(title), colours(title), codes(title), brand(brand_name), key(title))


def skeleton(code: str) -> str:
    """The letters of a code with each digit run replaced by '#': SN550 and SN750 are both 'sn#'."""
    return re.sub(r"\d+", "#", code)


def compatible(x: str, y: str) -> bool:
    """The same code, or one a longer form of the other (T6037 and T603700)."""
    return x.startswith(y) or y.startswith(x)


def near_miss(a: set[str], b: set[str]) -> tuple[str, str] | None:
    """Two codes of the same family that differ (SN550 vs SN750), when no code agrees."""
    if any(compatible(x, y) for x in a for y in b):
        return None
    for x in sorted(a):
        for y in sorted(b):
            if x[0].isalpha() and skeleton(x) == skeleton(y):
                return x, y
    return None


def same_amount(xs: list[float], ys: list[float]) -> bool:
    return any(abs(x - y) <= TOLERANCE * max(x, y) for x in xs for y in ys)


def names_in(brand_key: str, title_key: str) -> bool:
    return any(n in title_key for n in NAMES.get(brand_key, {brand_key}))


def veto(a: Facts, b: Facts) -> str | None:
    """Why two offers cannot be the same product, or None if their facts agree."""
    for kind in sorted(set(a.quantities) & set(b.quantities)):
        if not same_amount(a.quantities[kind], b.quantities[kind]):
            return f"{kind}: {_show(a.quantities[kind])} vs {_show(b.quantities[kind])}"
    if a.colours and b.colours and not a.colours & b.colours:
        return f"colour: {'/'.join(sorted(a.colours))} vs {'/'.join(sorted(b.colours))}"
    miss = near_miss(a.codes, b.codes)
    if miss:
        return f"model: {miss[0]} vs {miss[1]}"
    # Brand fields scraped from shops are noisy (distributors, URLs), so a brand only counts when
    # its own title names it and the other title does not.
    if (a.brand and b.brand and a.brand != b.brand
            and names_in(a.brand, a.title_key) and names_in(b.brand, b.title_key)
            and not names_in(a.brand, b.title_key) and not names_in(b.brand, a.title_key)):
        return f"brand: {a.brand} vs {b.brand}"
    return None


def agreement(a: Facts, b: Facts) -> dict[str, float]:
    """Features for the match scorer: which facts both titles state and share."""
    shared_q = any(same_amount(a.quantities[k], b.quantities[k]) for k in set(a.quantities) & set(b.quantities))
    return {
        "code_shared": float(any(compatible(x, y) for x in a.codes for y in b.codes)),
        "quantity_shared": float(shared_q),
        "colour_shared": float(bool(a.colours & b.colours)),
    }


def _show(values: list[float]) -> str:
    return ",".join(f"{v:g}" for v in sorted(set(values)))
