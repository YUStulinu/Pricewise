//! Normalisation rules for product titles, used as hard vetoes on matches.
//!
//! The same rules as `ml/pricewise_ml/rules.py`, with the same regular expressions; both are tested
//! against `rules/cases.json`. Embeddings are good at "these are both 1 TB WD SSDs" and bad at
//! "SN550 is not SN750" or "16 GB is not 64 GB"; these rules read the facts that decide identity out
//! of a title and veto a pair whose facts contradict each other. A rule only vetoes when both titles
//! state the fact.

use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::sync::LazyLock;

use regex::Regex;
use unicode_normalization::UnicodeNormalization;
use unicode_normalization::char::canonical_combining_class;

/// (quantity, multiplier to its base unit); "go" is the French gigaoctet.
fn unit(u: &str) -> Option<(&'static str, f64)> {
    Some(match u {
        "tb" => ("bytes", 1000.0),
        "gb" | "go" => ("bytes", 1.0),
        "mb" => ("bytes", 0.001),
        "l" => ("volume", 1000.0),
        "ml" => ("volume", 1.0),
        "cl" => ("volume", 10.0),
        "kg" => ("mass", 1000.0),
        "g" => ("mass", 1.0),
        "mah" => ("charge", 1.0),
        "w" => ("power", 1.0),
        "kw" => ("power", 1000.0),
        // Memory speeds; GHz is left out because shops quote a processor's base or boost clock.
        "mhz" => ("frequency", 1.0),
        _ => return None,
    })
}

static QUANTITY: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(\d+ ?x ?)?(\d+(?:[.,]\d+)?) ?(tb|gb|go|mb|mhz|ml|cl|kg|mah|kw|l|g|w)").unwrap());
static TOKEN: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"[a-z0-9]+(?:[-.][a-z0-9]+)*").unwrap());
static MEASURE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"^\d+(?:[.,]\d+)?(?:tb|gb|go|mb|mbs|mbps|gbps|gbs|mhz|ghz|hz|ml|cl|l|kg|g|mah|kw|w|v|mm|cm|m|in|p|k|x|st|nd|rd|th|tpi|c)$")
        .unwrap()
});
static WORD: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"[a-z]+").unwrap());
static DIGITS: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\d+").unwrap());

/// Two values of a quantity are the same within 5%: 8192 MB is 8 GB, 1 TB is 1024 GB.
const TOLERANCE: f64 = 0.05;

static COLOURS: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let table = [
        ("black", "black schwarz noir negro nero zwart svart czarny negru preto musta"),
        ("white", "white weiss blanc blanco blanca bianco wit hvid bialy alb branco valkoinen"),
        ("red", "red rot rouge rojo roja rosso rood czerwony rosu vermelho punainen"),
        ("blue", "blue blau bleu azul blu blauw niebieski albastru sininen"),
        ("green", "green grun vert verde groen zielony"),
        ("grey", "grey gray grau gris grigio grijs szary gri cinza harmaa"),
        ("silver", "silver silber argent plata argento zilver srebrny argintiu prata hopea"),
        ("gold", "gold oro goud guld zloty auriu dourado kulta"),
        ("pink", "pink rosa roze rozowy roz"),
        ("yellow", "yellow gelb jaune amarillo giallo geel gul zolty galben amarelo"),
        ("purple", "purple lila violet violeta viola paars fioletowy mov roxo"),
        ("orange", "orange naranja arancione oranje pomaranczowy portocaliu laranja"),
        ("brown", "brown braun marron marrone bruin brun brazowy maro castanho"),
    ];
    let mut m = HashMap::new();
    for (canonical, words) in table {
        for w in words.split_whitespace() {
            m.insert(w, canonical);
        }
    }
    m
});
const LINE_BRANDS: &[&str] = &["wd"];
const COLOURED_PARTS: &[&str] = &["led", "leds", "dot", "light", "laser"];

const BRAND_ALIASES: &[(&str, &str)] = &[
    ("wd", "westerndigital"),
    ("hgst", "westerndigital"),
    ("hp", "hewlettpackard"),
    ("hpe", "hewlettpackard"),
    ("hewlettpackardenterprise", "hewlettpackard"),
    ("lgelectronics", "lg"),
    ("samsungelectronics", "samsung"),
    ("kingstontechnology", "kingston"),
    ("hyperx", "kingston"),
    ("crucialtechnology", "crucial"),
    ("micron", "crucial"),
    ("appleinc", "apple"),
    ("logi", "logitech"),
    ("adata", "adata"),
    ("xpg", "adata"),
    ("raybansunglasses", "rayban"),
];

/// Lower case without accents ("Blanc" and "blánc" are the same word).
pub fn fold(text: &str) -> String {
    let lower = text.to_lowercase();
    let mut out = String::with_capacity(lower.len());
    for c in lower.nfkd() {
        if canonical_combining_class(c) != 0 {
            continue;
        }
        match c {
            'ß' => out.push_str("ss"),
            'ø' => out.push('o'),
            'æ' => out.push_str("ae"),
            'ł' => out.push('l'),
            'đ' => out.push('d'),
            _ => out.push(c),
        }
    }
    out
}

/// Letters and digits only: "Ray-Ban" and "rayban" have the same key.
pub fn key(text: &str) -> String {
    fold(text).chars().filter(|c| c.is_ascii_lowercase() || c.is_ascii_digit()).collect()
}

#[derive(Debug, Clone, Default)]
pub struct Facts {
    pub quantities: BTreeMap<&'static str, Vec<f64>>,
    pub colours: BTreeSet<&'static str>,
    pub codes: BTreeSet<String>,
    pub brand: Option<String>,
    pub title_key: String,
}

pub fn quantities(title: &str) -> BTreeMap<&'static str, Vec<f64>> {
    let text = fold(title);
    let mut out: BTreeMap<&'static str, Vec<f64>> = BTreeMap::new();
    for m in QUANTITY.captures_iter(&text) {
        let whole = m.get(0).unwrap();
        let before = text[..whole.start()].chars().next_back().unwrap_or(' ');
        let after = text[whole.end()..].chars().next().unwrap_or(' ');
        if before.is_alphanumeric() || before == '.' || before == ',' || after.is_alphanumeric() {
            continue;
        }
        let (kind, scale) = unit(&m[3]).unwrap();
        let Ok(number) = m[2].replace(',', ".").parse::<f64>() else { continue };
        let value = number * scale;
        let values = out.entry(kind).or_default();
        values.push(value);
        if let Some(kit) = m.get(1) {
            if let Ok(n) = kit.as_str().trim_end_matches([' ', 'x']).parse::<f64>() {
                values.push(value * n);
            }
        }
    }
    out
}

pub fn colours(title: &str) -> BTreeSet<&'static str> {
    let text = fold(title);
    let words: Vec<&str> = WORD.find_iter(&text).map(|m| m.as_str()).collect();
    let mut found = BTreeSet::new();
    for (i, w) in words.iter().enumerate() {
        let Some(c) = COLOURS.get(w) else { continue };
        if (i > 0 && LINE_BRANDS.contains(&words[i - 1])) || (i + 1 < words.len() && COLOURED_PARTS.contains(&words[i + 1])) {
            continue;
        }
        found.insert(*c);
    }
    found
}

fn is_code(token: &str) -> bool {
    token.chars().count() >= 3
        && token.chars().any(char::is_alphabetic)
        && token.chars().any(|c| c.is_ascii_digit())
        && !MEASURE.is_match(token)
}

pub fn codes(title: &str) -> BTreeSet<String> {
    let text = fold(title);
    TOKEN.find_iter(&text).map(|m| m.as_str()).filter(|t| is_code(t)).map(|t| t.replace(['-', '.'], "")).collect()
}

pub fn brand(name: Option<&str>) -> Option<String> {
    let k = key(name.unwrap_or(""));
    if k.is_empty() {
        return None;
    }
    Some(BRAND_ALIASES.iter().find(|(a, _)| *a == k).map(|(_, c)| c.to_string()).unwrap_or(k))
}

pub fn facts(title: &str, brand_name: Option<&str>) -> Facts {
    Facts {
        quantities: quantities(title),
        colours: colours(title),
        codes: codes(title),
        brand: brand(brand_name),
        title_key: key(title),
    }
}

/// The letters of a code with each digit run replaced by '#': SN550 and SN750 are both "sn#".
pub fn skeleton(code: &str) -> String {
    DIGITS.replace_all(code, "#").into_owned()
}

/// The same code, or one a longer form of the other (T6037 and T603700).
pub fn compatible(x: &str, y: &str) -> bool {
    x.starts_with(y) || y.starts_with(x)
}

/// Two codes of the same family that differ (SN550 vs SN750), when no code agrees.
pub fn near_miss<'a>(a: &'a BTreeSet<String>, b: &'a BTreeSet<String>) -> Option<(&'a str, &'a str)> {
    if a.iter().any(|x| b.iter().any(|y| compatible(x, y))) {
        return None;
    }
    for x in a {
        for y in b {
            if x.chars().next().is_some_and(char::is_alphabetic) && skeleton(x) == skeleton(y) {
                return Some((x, y));
            }
        }
    }
    None
}

fn same_amount(xs: &[f64], ys: &[f64]) -> bool {
    xs.iter().any(|x| ys.iter().any(|y| (x - y).abs() <= TOLERANCE * x.max(*y)))
}

fn names_in(brand_key: &str, title_key: &str) -> bool {
    title_key.contains(brand_key)
        || BRAND_ALIASES.iter().any(|(alias, canonical)| *canonical == brand_key && title_key.contains(alias))
}

/// Why two offers cannot be the same product, or None if their facts agree.
pub fn veto(a: &Facts, b: &Facts) -> Option<String> {
    for (kind, xs) in &a.quantities {
        if let Some(ys) = b.quantities.get(kind) {
            if !same_amount(xs, ys) {
                return Some(format!("{kind}: {} vs {}", show(xs), show(ys)));
            }
        }
    }
    if !a.colours.is_empty() && !b.colours.is_empty() && a.colours.is_disjoint(&b.colours) {
        let j = |s: &BTreeSet<&str>| s.iter().copied().collect::<Vec<_>>().join("/");
        return Some(format!("colour: {} vs {}", j(&a.colours), j(&b.colours)));
    }
    if let Some((x, y)) = near_miss(&a.codes, &b.codes) {
        return Some(format!("model: {x} vs {y}"));
    }
    // Brand fields scraped from shops are noisy (distributors, URLs), so a brand only counts when
    // its own title names it and the other title does not.
    if let (Some(x), Some(y)) = (&a.brand, &b.brand) {
        if x != y
            && names_in(x, &a.title_key)
            && names_in(y, &b.title_key)
            && !names_in(x, &b.title_key)
            && !names_in(y, &a.title_key)
        {
            return Some(format!("brand: {x} vs {y}"));
        }
    }
    None
}

/// Which facts both titles state and share: features for the scorer.
pub struct Agreement {
    pub code_shared: f64,
    pub quantity_shared: f64,
    pub colour_shared: f64,
}

pub fn agreement(a: &Facts, b: &Facts) -> Agreement {
    let quantity = a.quantities.iter().any(|(k, xs)| b.quantities.get(k).is_some_and(|ys| same_amount(xs, ys)));
    Agreement {
        code_shared: f64::from(a.codes.iter().any(|x| b.codes.iter().any(|y| compatible(x, y)))),
        quantity_shared: f64::from(quantity),
        colour_shared: f64::from(!a.colours.is_disjoint(&b.colours)),
    }
}

fn show(values: &[f64]) -> String {
    let mut v: Vec<f64> = values.to_vec();
    v.sort_by(f64::total_cmp);
    v.dedup();
    v.iter().map(|x| format!("{x}")).collect::<Vec<_>>().join(",")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(serde::Deserialize)]
    struct Case {
        a: String,
        b: String,
        brand_a: Option<String>,
        brand_b: Option<String>,
        veto: Option<String>,
    }

    #[test]
    fn shared_cases() {
        let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../rules/cases.json");
        let cases: Vec<Case> = serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap();
        assert!(cases.len() > 20);
        for c in cases {
            let why = veto(&facts(&c.a, c.brand_a.as_deref()), &facts(&c.b, c.brand_b.as_deref()));
            match (&c.veto, &why) {
                (None, None) => {}
                (Some(want), Some(got)) if got.starts_with(want.as_str()) => {}
                _ => panic!("{} | {}: want {:?}, got {:?}", c.a, c.b, c.veto, why),
            }
        }
    }

    #[test]
    fn units_and_kits() {
        assert_eq!(quantities("2TB and 500 GB")["bytes"], vec![2000.0, 500.0]);
        assert_eq!(quantities("0,5 l")["volume"], vec![500.0]);
        assert_eq!(quantities("Kit 2x8GB")["bytes"], vec![8.0, 16.0]);
        assert!(quantities("ASUS 90MB11B0").is_empty());
    }

    #[test]
    fn colours_and_codes() {
        assert!(colours("WD Blue SN550").is_empty());
        assert!(colours("Case, blue LED").is_empty());
        assert_eq!(colours("Corsair 275R Blanca").into_iter().collect::<Vec<_>>(), vec!["white"]);
        assert!(codes("Cruzer 16GB USB 3.0 1080p 100MB/s").is_empty());
        assert_eq!(codes("Logitech C310").into_iter().collect::<Vec<_>>(), vec!["c310"]);
        assert_eq!(fold("Weiß Grøn Blánc"), "weiss gron blanc");
    }
}
