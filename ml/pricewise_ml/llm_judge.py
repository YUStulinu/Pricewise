"""Asks a local LLM (Qwen2.5-7B-Instruct in llama-server) whether two offers are the same product.

Only the pairs the scorer is unsure about go to the LLM: it is slow (a second or so a pair) and
the embedder and rules settle the clear cases. The answer is constrained to JSON by a schema.
"""
from __future__ import annotations

import json
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor

import httpx

# The prompt is shared with the Rust engine (matcher/src/llm.rs).
PROMPT = (Path(__file__).resolve().parents[2] / "rules" / "llm_prompt.txt").read_text(encoding="utf-8")

SCHEMA = {"type": "object", "properties": {"reason": {"type": "string"}, "same": {"type": "boolean"}},
          "required": ["reason", "same"]}


class Judge:
    def __init__(self, url: str, workers: int = 4):
        self.url = url.rstrip("/")
        self.workers = workers
        self.client = httpx.Client(timeout=120)

    def same(self, a: str, b: str) -> bool | None:
        body = {
            "messages": [{"role": "user", "content": PROMPT.format(a=a, b=b)}],
            "temperature": 0, "max_tokens": 200,
            "response_format": {"type": "json_schema", "json_schema": {"name": "verdict", "schema": SCHEMA}},
        }
        try:
            r = self.client.post(f"{self.url}/v1/chat/completions", json=body)
            r.raise_for_status()
            return bool(json.loads(r.json()["choices"][0]["message"]["content"])["same"])
        except (httpx.HTTPError, KeyError, ValueError):
            return None

    def many(self, pairs: list[tuple[str, str]]) -> list[bool | None]:
        with ThreadPoolExecutor(self.workers) as pool:
            return list(pool.map(lambda p: self.same(*p), pairs))
