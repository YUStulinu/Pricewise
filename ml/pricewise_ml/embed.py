"""The title embedder: multilingual-e5-small, optionally fine-tuned on WDC Products pairs.

    python -m pricewise_ml.embed train      fine-tunes it and saves models/embedder
    python -m pricewise_ml.embed export     writes models/embedder/model.onnx for the Rust engine

The ONNX graph includes the mean pooling and the normalisation, so the Rust engine gets unit
vectors straight out of it: input_ids and attention_mask in, "embedding" (batch x 384) out.
"""
from __future__ import annotations

import argparse
import json
import random
import time
from pathlib import Path

import numpy as np
import torch
from transformers import AutoModel, AutoTokenizer

from pricewise_ml import wdc

BASE = "intfloat/multilingual-e5-small"
REVISION = "614241f622f53c4eeff9890bdc4f31cfecc418b3"
OUT = wdc.ROOT / "models" / "embedder"
PREFIX = "query: "  # e5 wants a prefix; "query: " is the one for symmetric similarity
MAX_TOKENS = 64


class Embedder(torch.nn.Module):
    def __init__(self, model):
        super().__init__()
        self.model = model

    def forward(self, input_ids, attention_mask):
        hidden = self.model(input_ids=input_ids, attention_mask=attention_mask).last_hidden_state
        mask = attention_mask.unsqueeze(-1).to(hidden.dtype)
        pooled = (hidden * mask).sum(1) / mask.sum(1).clamp(min=1e-9)
        return torch.nn.functional.normalize(pooled, dim=-1)


def load(path: str | Path | None = None, device: str | None = None):
    device = device or ("cuda" if torch.cuda.is_available() else "cpu")
    if path is None:
        tok = AutoTokenizer.from_pretrained(BASE, revision=REVISION)
        model = AutoModel.from_pretrained(BASE, revision=REVISION)
    else:
        tok = AutoTokenizer.from_pretrained(path)
        model = AutoModel.from_pretrained(path)
    return tok, Embedder(model).to(device).eval(), device


def text(title: str | None) -> str:
    return PREFIX + " ".join((title or "").split())


@torch.no_grad()
def encode(tok, emb, device, titles: list[str], batch: int = 256) -> np.ndarray:
    out = []
    for i in range(0, len(titles), batch):
        enc = tok([text(t) for t in titles[i:i + batch]], padding=True, truncation=True, max_length=MAX_TOKENS,
                  return_tensors="pt").to(device)
        out.append(emb(enc["input_ids"], enc["attention_mask"]).float().cpu().numpy())
    return np.concatenate(out) if out else np.zeros((0, 384), dtype=np.float32)


def train(epochs: int, margin: float, lr: float, batch: int, seed: int) -> None:
    random.seed(seed)
    torch.manual_seed(seed)
    tok, emb, device = load()
    # The benchmark's training split: 80% corner cases, large.
    pairs = [(r["title_left"] or "", r["title_right"] or "", float(r["label"])) for r in wdc.pairs(80, "train")]
    print(f"{len(pairs)} training pairs ({sum(p[2] for p in pairs):.0f} matches)", flush=True)
    emb.train()
    opt = torch.optim.AdamW(emb.parameters(), lr=lr, weight_decay=0.01)
    steps = epochs * (len(pairs) // batch)
    sched = torch.optim.lr_scheduler.OneCycleLR(opt, max_lr=lr, total_steps=steps, pct_start=0.1)
    t0 = time.time()
    step = 0
    for epoch in range(epochs):
        random.shuffle(pairs)
        total = 0.0
        for i in range(0, len(pairs) - batch + 1, batch):
            chunk = pairs[i:i + batch]
            a = tok([text(p[0]) for p in chunk], padding=True, truncation=True, max_length=MAX_TOKENS, return_tensors="pt").to(device)
            b = tok([text(p[1]) for p in chunk], padding=True, truncation=True, max_length=MAX_TOKENS, return_tensors="pt").to(device)
            y = torch.tensor([p[2] for p in chunk], device=device)
            with torch.autocast(device_type="cuda", dtype=torch.float16, enabled=device == "cuda"):
                ea = emb(a["input_ids"], a["attention_mask"])
                eb = emb(b["input_ids"], b["attention_mask"])
            d = 1 - (ea.float() * eb.float()).sum(-1)
            # Contrastive loss: pull matches together, push non-matches at least `margin` apart.
            loss = (y * d.pow(2) + (1 - y) * torch.relu(margin - d).pow(2)).mean()
            opt.zero_grad()
            loss.backward()
            torch.nn.utils.clip_grad_norm_(emb.parameters(), 1.0)
            opt.step()
            sched.step()
            total += loss.item()
            step += 1
            if step % 100 == 0:
                print(f"epoch {epoch + 1} step {step}/{steps} loss {total / ((i // batch) + 1):.4f} {time.time() - t0:.0f}s", flush=True)
    OUT.mkdir(parents=True, exist_ok=True)
    emb.model.save_pretrained(OUT)
    tok.save_pretrained(OUT)
    (OUT / "training.json").write_text(json.dumps({
        "base": BASE, "revision": REVISION, "pairs": len(pairs), "epochs": epochs, "margin": margin, "lr": lr,
        "batch": batch, "seed": seed, "seconds": round(time.time() - t0)}, indent=2))
    print(f"saved {OUT} in {time.time() - t0:.0f}s", flush=True)


def export(path: Path = OUT) -> None:
    import onnxruntime as ort

    tok, emb, _ = load(path, "cpu")
    enc = tok([text("WD Blue SN550 1TB"), text("Logitech C310 webcam HD 720p")], padding=True, return_tensors="pt")
    out = path / "model.onnx"
    torch.onnx.export(emb, (enc["input_ids"], enc["attention_mask"]), str(out),
                      input_names=["input_ids", "attention_mask"], output_names=["embedding"],
                      dynamic_axes={"input_ids": {0: "batch", 1: "tokens"}, "attention_mask": {0: "batch", 1: "tokens"},
                                    "embedding": {0: "batch"}},
                      opset_version=17, dynamo=False)
    # The ONNX model must give the same vectors as PyTorch.
    ref = emb(enc["input_ids"], enc["attention_mask"]).detach().numpy()
    sess = ort.InferenceSession(str(out), providers=["CPUExecutionProvider"])
    got = sess.run(None, {"input_ids": enc["input_ids"].numpy(), "attention_mask": enc["attention_mask"].numpy()})[0]
    print(f"exported {out} ({out.stat().st_size / 1e6:.0f} MB); max difference from PyTorch {np.abs(ref - got).max():.2e}")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("command", choices=["train", "export"])
    ap.add_argument("--epochs", type=int, default=3)
    ap.add_argument("--margin", type=float, default=0.5)
    ap.add_argument("--lr", type=float, default=3e-5)
    ap.add_argument("--batch", type=int, default=64)
    ap.add_argument("--seed", type=int, default=20)
    args = ap.parse_args()
    if args.command == "train":
        train(args.epochs, args.margin, args.lr, args.batch, args.seed)
    else:
        export()


if __name__ == "__main__":
    main()
