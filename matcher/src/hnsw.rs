//! Hierarchical navigable small world graphs (Malkov & Yashunin, 2018) for nearest-neighbour search
//! over unit vectors, where similarity is the dot product.
//!
//! Each point gets a random top layer (fewer points on each layer up); a search descends greedily
//! from the top and runs a beam search of width `ef` on layer 0. Neighbours are chosen with the
//! paper's heuristic, which keeps links pointing in different directions so the graph stays
//! navigable through clusters of near-duplicates (shops list many near-identical products).

use std::cmp::Ordering;
use std::collections::{BinaryHeap, HashSet};

#[derive(Clone, Copy, Debug)]
struct Scored {
    sim: f32,
    id: u32,
}
impl PartialEq for Scored {
    fn eq(&self, o: &Self) -> bool {
        self.sim == o.sim && self.id == o.id
    }
}
impl Eq for Scored {}
impl PartialOrd for Scored {
    fn partial_cmp(&self, o: &Self) -> Option<Ordering> {
        Some(self.cmp(o))
    }
}
impl Ord for Scored {
    fn cmp(&self, o: &Self) -> Ordering {
        self.sim.total_cmp(&o.sim).then(self.id.cmp(&o.id))
    }
}

pub struct Hnsw {
    dim: usize,
    data: Vec<f32>,
    /// links[layer][point] (a point has links on layers 0..=its level)
    links: Vec<Vec<Vec<u32>>>,
    levels: Vec<usize>,
    entry: Option<u32>,
    m: usize,
    m0: usize,
    ef_construction: usize,
    level_mult: f64,
    rng: u64,
}

pub fn dot(a: &[f32], b: &[f32]) -> f32 {
    a.iter().zip(b).map(|(x, y)| x * y).sum()
}

impl Hnsw {
    /// `m` links per point per layer (twice that on layer 0); `ef_construction` is the beam width
    /// used while inserting.
    pub fn new(dim: usize, m: usize, ef_construction: usize, seed: u64) -> Self {
        Hnsw {
            dim,
            data: Vec::new(),
            links: Vec::new(),
            levels: Vec::new(),
            entry: None,
            m,
            m0: 2 * m,
            ef_construction,
            level_mult: 1.0 / (m as f64).ln(),
            rng: seed | 1,
        }
    }

    pub fn len(&self) -> usize {
        self.levels.len()
    }

    pub fn is_empty(&self) -> bool {
        self.levels.is_empty()
    }

    pub fn vector(&self, id: u32) -> &[f32] {
        &self.data[id as usize * self.dim..(id as usize + 1) * self.dim]
    }

    fn random(&mut self) -> f64 {
        // xorshift64*: deterministic, so a build is reproducible
        self.rng ^= self.rng >> 12;
        self.rng ^= self.rng << 25;
        self.rng ^= self.rng >> 27;
        (self.rng.wrapping_mul(0x2545F4914F6CDD1D) >> 11) as f64 / (1u64 << 53) as f64
    }

    /// Adds a vector (already normalised) and returns its id.
    pub fn insert(&mut self, v: &[f32]) -> u32 {
        assert_eq!(v.len(), self.dim);
        let id = self.levels.len() as u32;
        let level = (-(1.0 - self.random()).ln() * self.level_mult).floor() as usize;
        self.data.extend_from_slice(v);
        self.levels.push(level);
        while self.links.len() <= level {
            self.links.push(Vec::new());
        }
        for layer in self.links.iter_mut() {
            layer.resize(id as usize + 1, Vec::new());
        }

        let Some(mut ep) = self.entry else {
            self.entry = Some(id);
            return id;
        };
        let top = self.levels[ep as usize];
        // Greedy descent through the layers above the new point's level.
        for layer in (level + 1..=top).rev() {
            ep = self.greedy(v, ep, layer);
        }
        let mut eps = vec![ep];
        for layer in (0..=level.min(top)).rev() {
            let found = self.search_layer(v, &eps, self.ef_construction, layer);
            let max = if layer == 0 { self.m0 } else { self.m };
            let chosen = self.select(&found, self.m);
            self.links[layer][id as usize] = chosen.clone();
            for &n in &chosen {
                self.links[layer][n as usize].push(id);
                if self.links[layer][n as usize].len() > max {
                    let nv = self.vector(n).to_vec();
                    let cands: Vec<Scored> = self.links[layer][n as usize]
                        .iter()
                        .map(|&x| Scored { sim: dot(&nv, self.vector(x)), id: x })
                        .collect();
                    self.links[layer][n as usize] = self.select(&cands, max);
                }
            }
            eps = found.iter().map(|s| s.id).collect();
        }
        if level > top {
            self.entry = Some(id);
        }
        id
    }

    fn greedy(&self, q: &[f32], mut ep: u32, layer: usize) -> u32 {
        let mut best = dot(q, self.vector(ep));
        loop {
            let mut moved = false;
            for &n in &self.links[layer][ep as usize] {
                let s = dot(q, self.vector(n));
                if s > best {
                    best = s;
                    ep = n;
                    moved = true;
                }
            }
            if !moved {
                return ep;
            }
        }
    }

    /// Beam search on one layer; returns up to `ef` points, most similar first.
    fn search_layer(&self, q: &[f32], eps: &[u32], ef: usize, layer: usize) -> Vec<Scored> {
        let mut visited: HashSet<u32> = eps.iter().copied().collect();
        let mut candidates: BinaryHeap<Scored> = BinaryHeap::new(); // best first
        let mut results: BinaryHeap<std::cmp::Reverse<Scored>> = BinaryHeap::new(); // worst first
        for &e in eps {
            let s = Scored { sim: dot(q, self.vector(e)), id: e };
            candidates.push(s);
            results.push(std::cmp::Reverse(s));
        }
        while let Some(c) = candidates.pop() {
            let worst = results.peek().unwrap().0.sim;
            if c.sim < worst && results.len() >= ef {
                break;
            }
            for &n in &self.links[layer][c.id as usize] {
                if !visited.insert(n) {
                    continue;
                }
                let s = Scored { sim: dot(q, self.vector(n)), id: n };
                if results.len() < ef || s.sim > results.peek().unwrap().0.sim {
                    candidates.push(s);
                    results.push(std::cmp::Reverse(s));
                    if results.len() > ef {
                        results.pop();
                    }
                }
            }
        }
        let mut out: Vec<Scored> = results.into_iter().map(|r| r.0).collect();
        out.sort_by(|a, b| b.cmp(a));
        out
    }

    /// The paper's neighbour heuristic: take candidates best first, skipping one that is closer to an
    /// already chosen neighbour than to the point itself; fill up with the skipped ones.
    fn select(&self, cands: &[Scored], m: usize) -> Vec<u32> {
        let mut sorted = cands.to_vec();
        sorted.sort_by(|a, b| b.cmp(a));
        let mut chosen: Vec<Scored> = Vec::with_capacity(m);
        let mut skipped = Vec::new();
        for c in sorted {
            if chosen.len() >= m {
                break;
            }
            let cv = self.vector(c.id);
            if chosen.iter().all(|x| dot(cv, self.vector(x.id)) < c.sim) {
                chosen.push(c);
            } else {
                skipped.push(c);
            }
        }
        for c in skipped {
            if chosen.len() >= m {
                break;
            }
            chosen.push(c);
        }
        chosen.into_iter().map(|s| s.id).collect()
    }

    /// The `k` most similar points to `q`, with their similarities, searching with beam width `ef`.
    pub fn search(&self, q: &[f32], k: usize, ef: usize) -> Vec<(u32, f32)> {
        let Some(mut ep) = self.entry else { return Vec::new() };
        for layer in (1..=self.levels[ep as usize]).rev() {
            ep = self.greedy(q, ep, layer);
        }
        self.search_layer(q, &[ep], ef.max(k), 0).into_iter().take(k).map(|s| (s.id, s.sim)).collect()
    }
}

/// Exact search, for measuring the graph's recall.
pub fn brute_force(data: &[Vec<f32>], q: &[f32], k: usize) -> Vec<(u32, f32)> {
    let mut all: Vec<(u32, f32)> = data.iter().enumerate().map(|(i, v)| (i as u32, dot(q, v))).collect();
    all.sort_by(|a, b| b.1.total_cmp(&a.1));
    all.truncate(k);
    all
}

#[cfg(test)]
mod tests {
    use super::*;

    fn random_unit(rng: &mut u64, dim: usize) -> Vec<f32> {
        let mut v: Vec<f32> = (0..dim)
            .map(|_| {
                *rng ^= *rng << 13;
                *rng ^= *rng >> 7;
                *rng ^= *rng << 17;
                (*rng % 2000) as f32 / 1000.0 - 1.0
            })
            .collect();
        let n = dot(&v, &v).sqrt();
        v.iter_mut().for_each(|x| *x /= n);
        v
    }

    #[test]
    fn recall_against_exact_search() {
        let mut rng = 42u64;
        // Clustered data, like many near-identical offers: 200 centres, 10 noisy copies each.
        let centres: Vec<Vec<f32>> = (0..200).map(|_| random_unit(&mut rng, 32)).collect();
        let mut data = Vec::new();
        for c in &centres {
            for _ in 0..10 {
                let noise = random_unit(&mut rng, 32);
                let mut v: Vec<f32> = c.iter().zip(&noise).map(|(a, b)| a + 0.3 * b).collect();
                let n = dot(&v, &v).sqrt();
                v.iter_mut().for_each(|x| *x /= n);
                data.push(v);
            }
        }
        let mut index = Hnsw::new(32, 12, 100, 7);
        for v in &data {
            index.insert(v);
        }
        let mut hits = 0;
        let queries = 200;
        for q in data.iter().step_by(data.len() / queries) {
            let exact: HashSet<u32> = brute_force(&data, q, 10).into_iter().map(|x| x.0).collect();
            hits += index.search(q, 10, 64).iter().filter(|x| exact.contains(&x.0)).count();
        }
        let recall = hits as f64 / (queries * 10) as f64;
        assert!(recall > 0.95, "recall@10 {recall}");
    }

    #[test]
    fn finds_itself_and_handles_tiny_indexes() {
        let mut index = Hnsw::new(2, 4, 16, 1);
        assert!(index.search(&[1.0, 0.0], 3, 8).is_empty());
        index.insert(&[1.0, 0.0]);
        index.insert(&[0.0, 1.0]);
        let r = index.search(&[0.0, 1.0], 5, 8);
        assert_eq!(r.len(), 2);
        assert_eq!(r[0].0, 1);
    }
}
