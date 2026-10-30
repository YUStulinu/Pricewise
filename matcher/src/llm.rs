//! Asks a local LLM (llama-server) about the pairs the scorer is unsure about. The prompt is shared
//! with the Python evaluation (rules/llm_prompt.txt).

use serde_json::{Value, json};

const PROMPT: &str = include_str!("../../rules/llm_prompt.txt");

pub struct Judge {
    url: String,
    agent: ureq::Agent,
}

impl Judge {
    pub fn new(url: &str) -> Self {
        let config = ureq::Agent::config_builder().timeout_global(Some(std::time::Duration::from_secs(120))).build();
        Judge { url: url.trim_end_matches('/').to_string(), agent: config.into() }
    }

    /// Whether the two titles are the same product; None if the LLM could not be asked.
    pub fn same(&self, a: &str, b: &str) -> Option<bool> {
        let body = json!({
            "messages": [{"role": "user", "content": PROMPT.trim_end().replace("{a}", a).replace("{b}", b)}],
            "temperature": 0, "max_tokens": 200,
            "response_format": {"type": "json_schema", "json_schema": {"name": "verdict", "schema": {
                "type": "object",
                "properties": {"reason": {"type": "string"}, "same": {"type": "boolean"}},
                "required": ["reason", "same"]}}},
        });
        let reply: Value = self
            .agent
            .post(format!("{}/v1/chat/completions", self.url))
            .send_json(body)
            .ok()?
            .body_mut()
            .read_json()
            .ok()?;
        let content = reply["choices"][0]["message"]["content"].as_str()?;
        serde_json::from_str::<Value>(content).ok()?["same"].as_bool()
    }
}
