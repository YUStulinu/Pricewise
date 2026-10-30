//! The match scorer trained in Python (ml/pricewise_ml/scorer.py): a logistic regression over the
//! embedding similarity and the rule features, applied with the saved weights.

use std::collections::HashSet;
use std::path::Path;
use std::sync::LazyLock;

use regex::Regex;
use serde::Deserialize;

use crate::rules::{self, Facts};

const FEATURES: [&str; 5] = ["cosine", "token_jaccard", "code_shared", "quantity_shared", "colour_shared"];
static TOKEN: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"[a-z0-9]+").unwrap());

#[derive(Deserialize, Debug, Clone)]
pub struct Scorer {
    features: Vec<String>,
    weights: Vec<f64>,
    bias: f64,
    pub threshold: f64,
    pub borderline: (f64, f64),
}

impl Scorer {
    pub fn load(path: &Path) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        let s: Scorer = serde_json::from_str(&std::fs::read_to_string(path)?)?;
        if s.features != FEATURES {
            return Err(format!("{}: features {:?}, expected {:?}", path.display(), s.features, FEATURES).into());
        }
        Ok(s)
    }

    pub fn probability(&self, x: &[f64; 5]) -> f64 {
        let z = self.bias + self.weights.iter().zip(x).map(|(w, v)| w * v).sum::<f64>();
        1.0 / (1.0 + (-z).exp())
    }
}

fn tokens(title: &str) -> HashSet<String> {
    TOKEN.find_iter(&rules::fold(title)).map(|m| m.as_str().to_string()).collect()
}

pub fn features(cosine: f32, a_title: &str, b_title: &str, a: &Facts, b: &Facts) -> [f64; 5] {
    let (ta, tb) = (tokens(a_title), tokens(b_title));
    let union = ta.union(&tb).count();
    let jaccard = if union == 0 { 0.0 } else { ta.intersection(&tb).count() as f64 / union as f64 };
    let agree = rules::agreement(a, b);
    [f64::from(cosine), jaccard, agree.code_shared, agree.quantity_shared, agree.colour_shared]
}
