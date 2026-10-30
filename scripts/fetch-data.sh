#!/usr/bin/env bash
# Downloads what Pricewise uses but does not keep in git and checks each file's SHA-256:
#   - WDC Products (80% corner cases: pairs, and offers by product) into data/raw/wdc;
#   - with --llm: llama.cpp's server b11429 and Qwen2.5-7B-Instruct Q4_K_M (4.7 GB) into third_party/,
#     for asking about borderline pairs.
# The embedder is not downloaded: it is fine-tuned from multilingual-e5-small (downloaded by the
# training script, pinned to a revision) with `python -m pricewise_ml.embed train`.
#   scripts/fetch-data.sh [--llm]
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
llm=0
for a in "$@"; do
  case "$a" in
    --llm) llm=1 ;;
    *) echo "unknown option $a" >&2; exit 2 ;;
  esac
done

fetch() { # url sha256 file
  local url="$1" sha="$2" file="$3"
  mkdir -p "$(dirname "$file")"
  if [ ! -f "$file" ] || ! echo "$sha  $file" | sha256sum -c - >/dev/null 2>&1; then
    echo "downloading $url"
    curl -fsSL --retry 3 -o "$file.part" "$url" || { echo "download failed: $url" >&2; exit 1; }
    echo "$sha  $file.part" | sha256sum -c - >/dev/null || { echo "checksum mismatch: $url" >&2; rm -f "$file.part"; exit 1; }
    mv "$file.part" "$file"
  fi
}

wdc="https://data.dws.informatik.uni-mannheim.de/largescaleproductcorpus/data/wdc-products"
raw="$root/data/raw/wdc"
fetch "$wdc/80pair.zip" b2044939cee5ea6f12148a2f3551508de3cb77660dfc91767c44daaf9d8a9c4a "$raw/80pair.zip"
fetch "$wdc/80multi.zip" 5385e2491e82c7f6c208d8eac7ec777a4a44180769f3a339c99803ff2b068981 "$raw/80multi.zip"
for z in 80pair 80multi; do
  [ -d "$raw/$z" ] || unzip -qo "$raw/$z.zip" -d "$raw/$z"
done

if [ $llm = 1 ]; then
  tp="$root/third_party"
  dl="$tp/downloads"
  llama="https://github.com/ggml-org/llama.cpp/releases/download/b11429"
  case "$(uname -s)" in
    MINGW* | MSYS* | CYGWIN*)
      fetch "$llama/llama-b11429-bin-win-cuda-13.4-x64.zip" 76ddc6eff2389570789ed608881efc6977751a722015d8e8c94f302224ff1a3a "$dl/llama-win.zip"
      fetch "$llama/cudart-llama-bin-win-cuda-13.4-x64.zip" 738f8c251ac22b70c3ae6f83a10cf222725df0395246a2cf58f32bdb85fbe668 "$dl/cudart-win.zip"
      if [ ! -f "$tp/llama/llama-server.exe" ]; then
        mkdir -p "$tp/llama"
        for z in llama-win.zip cudart-win.zip; do unzip -qo "$dl/$z" -d "$tp/llama"; done
      fi ;;
    Linux)
      fetch "$llama/llama-b11429-bin-ubuntu-x64.tar.gz" f6d25dde8f51133143d1453da4fd5f73b145127177612a283bf7995957af3392 "$dl/llama-linux.tar.gz"
      if [ ! -x "$tp/llama/llama-server" ]; then
        mkdir -p "$tp/llama"
        tar -xzf "$dl/llama-linux.tar.gz" -C "$tp/llama" --strip-components=1
      fi ;;
  esac
  fetch https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF/resolve/main/Qwen2.5-7B-Instruct-Q4_K_M.gguf \
    65b8fcd92af6b4fefa935c625d1ac27ea29dcb6ee14589c55a8f115ceaaa1423 "$tp/models/Qwen2.5-7B-Instruct-Q4_K_M.gguf"
fi
echo "done"
