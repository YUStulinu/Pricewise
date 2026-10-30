//! Pricewise matching engine: which offers from different shops are the same product.

pub mod db;
pub mod embed;
pub mod engine;
pub mod hnsw;
pub mod llm;
pub mod rules;
pub mod scorer;
