//! Matching offers into products: candidates from the HNSW index, a decision per candidate pair,
//! then groups.
//!
//! For each offer the index gives its nearest titles; pairs from the same shop are skipped (a shop
//! lists a product once). Each candidate pair is vetoed by the rules or scored; pairs in the
//! scorer's borderline band can go to the LLM. Groups are then built greedily, most confident pair
//! first, never putting two offers of one shop into the same product.

use crate::hnsw::Hnsw;
use crate::llm::Judge;
use crate::rules::{self, Facts};
use crate::scorer::{self, Scorer};

#[derive(Debug, Clone)]
pub struct Offer {
    pub id: i64,
    pub shop: i64,
    pub title: String,
    pub brand: Option<String>,
}

#[derive(Debug, Clone)]
pub struct Decision {
    pub a: usize,
    pub b: usize,
    pub cosine: f32,
    pub probability: f64,
    pub veto: Option<String>,
    pub llm: Option<bool>,
    pub same: bool,
}

pub struct Settings {
    pub neighbours: usize,
    pub ef: usize,
    pub min_cosine: f32,
}

impl Default for Settings {
    fn default() -> Self {
        Settings { neighbours: 10, ef: 64, min_cosine: 0.5 }
    }
}

/// Builds the index over the offers' embeddings.
pub fn index(embeddings: &[Vec<f32>]) -> Hnsw {
    let mut h = Hnsw::new(embeddings.first().map_or(1, Vec::len), 16, 200, 20);
    for v in embeddings {
        h.insert(v);
    }
    h
}

/// Candidate pairs (a < b) from different shops among each offer's nearest neighbours.
pub fn candidates(offers: &[Offer], embeddings: &[Vec<f32>], idx: &Hnsw, s: &Settings) -> Vec<(usize, usize, f32)> {
    let mut seen = std::collections::HashSet::new();
    let mut out = Vec::new();
    for (i, v) in embeddings.iter().enumerate() {
        for (j, sim) in idx.search(v, s.neighbours + 1, s.ef) {
            let j = j as usize;
            if j == i || offers[i].shop == offers[j].shop || sim < s.min_cosine {
                continue;
            }
            let key = (i.min(j), i.max(j));
            if seen.insert(key) {
                out.push((key.0, key.1, sim));
            }
        }
    }
    out
}

/// Decides one pair: the rules first, then the scorer, then (in the borderline band) the LLM.
pub fn decide(
    a: (&str, &Facts),
    b: (&str, &Facts),
    cosine: f32,
    scorer: &Scorer,
    judge: Option<&Judge>,
) -> (f64, Option<String>, Option<bool>, bool) {
    if let Some(why) = rules::veto(a.1, b.1) {
        return (0.0, Some(why), None, false);
    }
    let p = scorer.probability(&scorer::features(cosine, a.0, b.0, a.1, b.1));
    let mut same = p >= scorer.threshold;
    let mut asked = None;
    if let Some(j) = judge {
        if p >= scorer.borderline.0 && p <= scorer.borderline.1 {
            asked = j.same(a.0, b.0);
            if let Some(v) = asked {
                same = v;
            }
        }
    }
    (p, None, asked, same)
}

pub fn decide_all(
    offers: &[Offer],
    pairs: &[(usize, usize, f32)],
    scorer: &Scorer,
    judge: Option<&Judge>,
    workers: usize,
) -> Vec<Decision> {
    let facts: Vec<Facts> = offers.iter().map(|o| rules::facts(&o.title, o.brand.as_deref())).collect();
    // The LLM is slow and llama-server answers several requests at once, so decide in parallel.
    let chunk = pairs.len().div_ceil(workers.max(1)).max(1);
    std::thread::scope(|scope| {
        let handles: Vec<_> = pairs
            .chunks(chunk)
            .map(|part| {
                let facts = &facts;
                scope.spawn(move || {
                    part.iter()
                        .map(|&(a, b, cosine)| {
                            let (probability, veto, llm, same) = decide(
                                (&offers[a].title, &facts[a]),
                                (&offers[b].title, &facts[b]),
                                cosine,
                                scorer,
                                judge,
                            );
                            Decision { a, b, cosine, probability, veto, llm, same }
                        })
                        .collect::<Vec<_>>()
                })
            })
            .collect();
        handles.into_iter().flat_map(|h| h.join().unwrap()).collect()
    })
}

/// Groups offers into products: union of "same" pairs, most confident first, never two offers of
/// one shop in a group. Returns a group number per offer.
pub fn group(offers: &[Offer], decisions: &[Decision]) -> Vec<usize> {
    let n = offers.len();
    let mut parent: Vec<usize> = (0..n).collect();
    let mut shops: Vec<std::collections::BTreeSet<i64>> = offers.iter().map(|o| [o.shop].into()).collect();
    fn root(p: &mut [usize], mut x: usize) -> usize {
        while p[x] != x {
            p[x] = p[p[x]];
            x = p[x];
        }
        x
    }
    let mut same: Vec<&Decision> = decisions.iter().filter(|d| d.same).collect();
    // LLM-confirmed pairs count as most confident, then by probability.
    same.sort_by(|x, y| {
        (y.llm == Some(true)).cmp(&(x.llm == Some(true))).then(y.probability.total_cmp(&x.probability))
    });
    for d in same {
        let (ra, rb) = (root(&mut parent, d.a), root(&mut parent, d.b));
        if ra == rb || !shops[ra].is_disjoint(&shops[rb]) {
            continue;
        }
        parent[rb] = ra;
        let moved = std::mem::take(&mut shops[rb]);
        shops[ra].extend(moved);
    }
    (0..n).map(|i| root(&mut parent, i)).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn offer(id: i64, shop: i64, title: &str) -> Offer {
        Offer { id, shop, title: title.into(), brand: None }
    }

    fn decision(a: usize, b: usize, p: f64) -> Decision {
        Decision { a, b, cosine: 0.9, probability: p, veto: None, llm: None, same: true }
    }

    #[test]
    fn groups_never_hold_two_offers_of_one_shop() {
        let offers = vec![offer(1, 1, "x"), offer(2, 2, "x"), offer(3, 1, "x'"), offer(4, 3, "x")];
        // 0-1 is the surest pair; 1-2 would put two offers of shop 1 together.
        let g = group(&offers, &[decision(0, 1, 0.99), decision(1, 2, 0.9), decision(1, 3, 0.8)]);
        assert_eq!(g[0], g[1]);
        assert_ne!(g[1], g[2]);
        assert_eq!(g[3], g[0]);
    }

    #[test]
    fn rules_veto_before_the_scorer() {
        let s: Scorer = serde_json::from_str(
            r#"{"features":["cosine","token_jaccard","code_shared","quantity_shared","colour_shared"],
                "weights":[10,0,0,0,0],"bias":-5,"threshold":0.5,"borderline":[0.4,0.6]}"#,
        )
        .unwrap();
        let (a, b) = ("Logitech C310 webcam", "Logitech C920 webcam");
        let (fa, fb) = (rules::facts(a, None), rules::facts(b, None));
        let (p, veto, _, same) = decide((a, &fa), (b, &fb), 0.99, &s, None);
        assert!(veto.unwrap().starts_with("model") && !same && p == 0.0);
        let (c, fc) = ("Logitech HD webcam C310", rules::facts("Logitech HD webcam C310", None));
        let (_, veto, _, same) = decide((a, &fa), (c, &fc), 0.95, &s, None);
        assert!(veto.is_none() && same);
    }
}
