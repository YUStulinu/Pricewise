//! pricewise-match: groups the collected offers into products.
//!
//!   pricewise-match run    --db postgres://... [--llm http://127.0.0.1:8094]
//!   pricewise-match pairs  --input pairs.jsonl --output decisions.jsonl [--llm ...]
//!   pricewise-match bench  --input titles.txt
//!
//! `run` matches every offer in the database; `pairs` decides labelled pairs (for the evaluation
//! and for checking the engine agrees with the Python implementation); `bench` measures the HNSW
//! index against exact search.

use std::collections::HashSet;
use std::io::{BufRead, Write};
use std::path::PathBuf;
use std::time::Instant;

use clap::{Parser, Subcommand};
use serde::Deserialize;
use serde_json::json;

use pricewise_matcher::embed::{Embedder, Error};
use pricewise_matcher::engine::{self, Offer, Settings};
use pricewise_matcher::hnsw::{self, Hnsw};
use pricewise_matcher::llm::Judge;
use pricewise_matcher::rules;
use pricewise_matcher::scorer::Scorer;
use pricewise_matcher::db;

#[derive(Parser)]
#[command(about = "Pricewise matching engine")]
struct Cli {
    /// The exported embedder (model.onnx and tokenizer.json)
    #[arg(long, default_value = "models/embedder", global = true)]
    model: PathBuf,
    /// The scorer's weights
    #[arg(long, default_value = "models/scorer.json", global = true)]
    scorer: PathBuf,
    /// llama-server address, to ask about borderline pairs
    #[arg(long, global = true)]
    llm: Option<String>,
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Match all offers in the database into products
    Run {
        #[arg(long, env = "PRICEWISE_DB", default_value = "postgres://pricewise:pricewise@127.0.0.1:5433/pricewise")]
        db: String,
        #[arg(long, default_value_t = 10)]
        neighbours: usize,
    },
    /// Decide labelled pairs read as JSON lines {"a", "b", "brand_a", "brand_b"}
    Pairs {
        #[arg(long)]
        input: PathBuf,
        #[arg(long)]
        output: PathBuf,
    },
    /// Measure HNSW recall and speed against exact search on titles, one per line
    Bench {
        #[arg(long)]
        input: PathBuf,
    },
}

#[derive(Deserialize)]
struct Pair {
    a: String,
    b: String,
    brand_a: Option<String>,
    brand_b: Option<String>,
}

fn main() -> Result<(), Error> {
    let cli = Cli::parse();
    let judge = cli.llm.as_deref().map(Judge::new);
    match cli.command {
        Command::Run { db: url, neighbours } => {
            let t0 = Instant::now();
            let mut conn = db::connect(&url)?;
            let (offers, current) = db::offers(&mut conn)?;
            if offers.is_empty() {
                println!("{}", json!({"offers": 0}));
                return Ok(());
            }
            let scorer = Scorer::load(&cli.scorer)?;
            let mut embedder = Embedder::load(&cli.model)?;
            let titles: Vec<String> = offers.iter().map(|o| o.title.clone()).collect();
            let embeddings = embedder.embed(&titles)?;
            let t_embed = t0.elapsed();
            let idx = engine::index(&embeddings);
            let settings = Settings { neighbours, ..Settings::default() };
            let pairs = engine::candidates(&offers, &embeddings, &idx, &settings);
            let t_index = t0.elapsed();
            let decisions = engine::decide_all(&offers, &pairs, &scorer, judge.as_ref(), if judge.is_some() { 4 } else { 1 });
            let groups = engine::group(&offers, &decisions);
            let written = db::save(&mut conn, &offers, &current, &groups, &decisions, &embeddings)?;
            println!(
                "{}",
                json!({
                    "offers": offers.len(),
                    "candidate_pairs": pairs.len(),
                    "vetoed": decisions.iter().filter(|d| d.veto.is_some()).count(),
                    "asked_llm": decisions.iter().filter(|d| d.llm.is_some()).count(),
                    "same": decisions.iter().filter(|d| d.same).count(),
                    "products": written.products,
                    "products_kept": written.kept,
                    "products_created": written.created,
                    "products_removed": written.removed,
                    "seconds_embedding": t_embed.as_secs_f64(),
                    "seconds_index_and_search": (t_index - t_embed).as_secs_f64(),
                    "seconds_total": t0.elapsed().as_secs_f64(),
                })
            );
        }
        Command::Pairs { input, output } => {
            let scorer = Scorer::load(&cli.scorer)?;
            let mut embedder = Embedder::load(&cli.model)?;
            let file = std::io::BufReader::new(std::fs::File::open(&input)?);
            let pairs: Vec<Pair> = file
                .lines()
                .map_while(Result::ok)
                .filter(|l| !l.trim().is_empty())
                .map(|l| serde_json::from_str(&l))
                .collect::<Result<_, _>>()?;
            let ea = embedder.embed(&pairs.iter().map(|p| p.a.clone()).collect::<Vec<_>>())?;
            let eb = embedder.embed(&pairs.iter().map(|p| p.b.clone()).collect::<Vec<_>>())?;
            let offers: Vec<Offer> = pairs
                .iter()
                .enumerate()
                .flat_map(|(i, p)| {
                    [
                        Offer { id: 2 * i as i64, shop: 0, title: p.a.clone(), brand: p.brand_a.clone() },
                        Offer { id: 2 * i as i64 + 1, shop: 1, title: p.b.clone(), brand: p.brand_b.clone() },
                    ]
                })
                .collect();
            let cand: Vec<(usize, usize, f32)> =
                (0..pairs.len()).map(|i| (2 * i, 2 * i + 1, hnsw::dot(&ea[i], &eb[i]))).collect();
            let decisions = engine::decide_all(&offers, &cand, &scorer, judge.as_ref(), if judge.is_some() { 4 } else { 1 });
            let mut out = std::io::BufWriter::new(std::fs::File::create(&output)?);
            for d in decisions {
                writeln!(
                    out,
                    "{}",
                    json!({"cosine": d.cosine, "probability": d.probability, "veto": d.veto, "llm": d.llm, "same": d.same})
                )?;
            }
        }
        Command::Bench { input } => {
            let titles: Vec<String> =
                std::fs::read_to_string(&input)?.lines().filter(|l| !l.trim().is_empty()).map(String::from).collect();
            let mut embedder = Embedder::load(&cli.model)?;
            let t0 = Instant::now();
            let vectors = embedder.embed(&titles)?;
            let t_embed = t0.elapsed().as_secs_f64();
            let t1 = Instant::now();
            let mut idx = Hnsw::new(vectors[0].len(), 16, 200, 20);
            for v in &vectors {
                idx.insert(v);
            }
            let t_build = t1.elapsed().as_secs_f64();
            let queries: Vec<&Vec<f32>> = vectors.iter().step_by((vectors.len() / 1000).max(1)).collect();
            let mut report = Vec::new();
            for ef in [16, 32, 64, 128] {
                let t = Instant::now();
                let mut hits = 0;
                for q in &queries {
                    let found: HashSet<u32> = idx.search(q, 10, ef).into_iter().map(|x| x.0).collect();
                    hits += hnsw::brute_force(&vectors, q, 10).iter().filter(|x| found.contains(&x.0)).count();
                }
                // Time the index alone (exact search above is only for the recall).
                let t_search = {
                    let t2 = Instant::now();
                    for q in &queries {
                        std::hint::black_box(idx.search(q, 10, ef));
                    }
                    t2.elapsed().as_secs_f64()
                };
                let _ = t;
                report.push(json!({"ef": ef, "recall_at_10": hits as f64 / (queries.len() * 10) as f64,
                                   "microseconds_per_query": t_search / queries.len() as f64 * 1e6}));
            }
            let t3 = Instant::now();
            for q in &queries {
                std::hint::black_box(hnsw::brute_force(&vectors, q, 10));
            }
            let exact = t3.elapsed().as_secs_f64() / queries.len() as f64 * 1e6;
            println!(
                "{}",
                serde_json::to_string_pretty(&json!({
                    "titles": titles.len(), "queries": queries.len(), "seconds_embedding": t_embed,
                    "titles_per_second": titles.len() as f64 / t_embed, "seconds_build": t_build,
                    "exact_microseconds_per_query": exact, "hnsw": report,
                }))?
            );
        }
    }
    let _ = rules::fold; // the rules are used through the engine
    Ok(())
}
