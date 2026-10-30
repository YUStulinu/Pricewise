//! Reads offers from PostgreSQL and writes the products the engine grouped them into.

use std::collections::HashMap;

use postgres::{Client, NoTls};

use crate::embed::Error;
use crate::engine::{Decision, Offer};

pub fn connect(url: &str) -> Result<Client, Error> {
    Ok(Client::connect(url, NoTls)?)
}

pub fn offers(db: &mut Client) -> Result<(Vec<Offer>, Vec<Option<i32>>), Error> {
    let rows = db.query("SELECT id, shop_id, title, brand, product_id FROM offers ORDER BY id", &[])?;
    let offers = rows
        .iter()
        .map(|r| Offer {
            id: i64::from(r.get::<_, i32>(0)),
            shop: i64::from(r.get::<_, i32>(1)),
            title: r.get(2),
            brand: r.get(3),
        })
        .collect();
    Ok((offers, rows.iter().map(|r| r.get(4)).collect()))
}

pub struct Written {
    pub products: usize,
    pub kept: usize,
    pub created: usize,
    pub removed: u64,
}

/// Saves the grouping. A group keeps the product id most of its offers already had, so products
/// (and the alerts set on them) survive a re-run; products left without offers are removed.
pub fn save(
    db: &mut Client,
    offers: &[Offer],
    current: &[Option<i32>],
    groups: &[usize],
    decisions: &[Decision],
    embeddings: &[Vec<f32>],
) -> Result<Written, Error> {
    let mut members: HashMap<usize, Vec<usize>> = HashMap::new();
    for (i, g) in groups.iter().enumerate() {
        members.entry(*g).or_default().push(i);
    }
    // Each offer's best "same" decision inside its group.
    let mut best: HashMap<usize, (f64, bool)> = HashMap::new();
    for d in decisions.iter().filter(|d| d.same && groups[d.a] == groups[d.b]) {
        for i in [d.a, d.b] {
            let e = best.entry(i).or_insert((0.0, false));
            e.0 = e.0.max(d.probability);
            e.1 |= d.llm.is_some();
        }
    }

    let mut tx = db.transaction()?;
    let mut taken = std::collections::HashSet::new();
    let (mut kept, mut created) = (0, 0);
    let mut keys: Vec<usize> = members.keys().copied().collect();
    keys.sort_by_key(|g| std::cmp::Reverse(members[g].len())); // big groups choose their id first
    for g in keys {
        let list = &members[&g];
        let mut votes: HashMap<i32, usize> = HashMap::new();
        for &i in list {
            if let Some(p) = current[i] {
                *votes.entry(p).or_default() += 1;
            }
        }
        let mut ranked: Vec<(i32, usize)> = votes.into_iter().collect();
        ranked.sort_by(|a, b| b.1.cmp(&a.1).then(a.0.cmp(&b.0)));
        let product = match ranked.into_iter().map(|x| x.0).find(|p| !taken.contains(p)) {
            Some(p) => {
                kept += 1;
                p
            }
            None => {
                created += 1;
                let title = &offers[medoid(list, embeddings)].title;
                tx.query_one("INSERT INTO products (title) VALUES ($1) RETURNING id", &[title])?.get(0)
            }
        };
        taken.insert(product);
        for &i in list {
            let (score, method) = match best.get(&i) {
                Some(&(p, llm)) => (Some(p as f32), if llm { "model+llm" } else { "model" }),
                None => (None, "single"),
            };
            tx.execute(
                "UPDATE offers SET product_id = $1, match_score = $2, match_method = $3 WHERE id = $4",
                &[&product, &score, &method, &(offers[i].id as i32)],
            )?;
        }
    }
    let removed = tx.execute("DELETE FROM products p WHERE NOT EXISTS (SELECT 1 FROM offers o WHERE o.product_id = p.id)", &[])?;
    tx.commit()?;
    Ok(Written { products: members.len(), kept, created, removed })
}

/// The member most similar to the rest: its title names the product.
fn medoid(list: &[usize], embeddings: &[Vec<f32>]) -> usize {
    *list
        .iter()
        .max_by(|&&x, &&y| {
            let s = |i: usize| list.iter().map(|&j| crate::hnsw::dot(&embeddings[i], &embeddings[j])).sum::<f32>();
            s(x).total_cmp(&s(y))
        })
        .unwrap()
}
