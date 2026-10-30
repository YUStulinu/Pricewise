//! Title embeddings from the fine-tuned multilingual-e5-small, exported to ONNX by
//! `python -m pricewise_ml.embed export`. The graph includes mean pooling and normalisation, so it
//! returns unit vectors.

use std::path::Path;

use ort::session::Session;
use ort::session::builder::GraphOptimizationLevel;
use ort::value::Tensor;
use tokenizers::{PaddingParams, PaddingStrategy, Tokenizer, TruncationParams};

pub const DIM: usize = 384;
// The same prefix and length limit as ml/pricewise_ml/embed.py.
const PREFIX: &str = "query: ";
const MAX_TOKENS: usize = 64;

pub struct Embedder {
    session: Session,
    tokenizer: Tokenizer,
    _environment: ort::environment::Environment,
}

pub type Error = Box<dyn std::error::Error + Send + Sync>;

fn session(environment: &ort::environment::Environment, model: &Path) -> ort::Result<Session> {
    let threads = std::thread::available_parallelism().map(|n| n.get()).unwrap_or(4).min(8);
    Ok(Session::builder(environment)?
        .with_optimization_level(GraphOptimizationLevel::Level3)?
        .with_intra_threads(threads)?
        .commit_from_file(model)?)
}

impl Embedder {
    pub fn load(dir: &Path) -> Result<Self, Error> {
        let mut tokenizer = Tokenizer::from_file(dir.join("tokenizer.json"))?;
        let pad_id = tokenizer.token_to_id("<pad>").unwrap_or(1);
        tokenizer.with_padding(Some(PaddingParams {
            strategy: PaddingStrategy::BatchLongest,
            pad_id,
            pad_token: "<pad>".into(),
            ..Default::default()
        }));
        tokenizer
            .with_truncation(Some(TruncationParams { max_length: MAX_TOKENS, ..Default::default() }))
            .map_err(|e| e.to_string())?;
        let environment = ort::init().with_name("pricewise").build()?;
        let session = session(&environment, &dir.join("model.onnx"))?;
        Ok(Embedder { session, tokenizer, _environment: environment })
    }

    /// Unit vectors for the titles, DIM floats each.
    pub fn embed(&mut self, titles: &[String]) -> Result<Vec<Vec<f32>>, Error> {
        let mut out = Vec::with_capacity(titles.len());
        for chunk in titles.chunks(128) {
            let texts: Vec<String> =
                chunk.iter().map(|t| format!("{PREFIX}{}", t.split_whitespace().collect::<Vec<_>>().join(" "))).collect();
            let enc = self.tokenizer.encode_batch(texts, true).map_err(|e| e.to_string())?;
            let (rows, cols) = (enc.len(), enc[0].get_ids().len());
            let ids: Vec<i64> = enc.iter().flat_map(|e| e.get_ids().iter().map(|&x| x as i64)).collect();
            let mask: Vec<i64> = enc.iter().flat_map(|e| e.get_attention_mask().iter().map(|&x| x as i64)).collect();
            let outputs = self.session.run(ort::inputs![
                "input_ids" => Tensor::from_array(([rows, cols], ids))?,
                "attention_mask" => Tensor::from_array(([rows, cols], mask))?,
            ])?;
            let (_, data) = outputs["embedding"].try_extract_tensor::<f32>()?;
            out.extend(data.chunks(DIM).map(<[f32]>::to_vec));
        }
        Ok(out)
    }
}
