#!/usr/bin/env bash
# The whole pipeline on the simulated shops: database, shops, 120 days of collecting, matching,
# fake-discount detection, then the dashboard at http://localhost:3020.
# Needs: scripts/fetch-data.sh, the ml virtual environment, and a trained embedder
# (python -m pricewise_ml.embed train && python -m pricewise_ml.embed export) plus models/scorer.json
# (python -m pricewise_ml.evaluate_matching).
#   scripts/demo.sh [--llm http://127.0.0.1:8094]
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
py=ml/.venv/bin/python; [ -x "$py" ] || py=ml/.venv/Scripts/python
exe=""; case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) exe=".exe" ;; esac

docker compose up -d --wait
(cd ml && "../$py" -m pricewise_ml.simulate)
(cd collector && go build -o ../bin/ ./cmd/...)
(cd matcher && cargo build --release)

bin/shopsim$exe -catalogue data/sim/catalogue.json &
shops=$!
trap 'kill $shops 2>/dev/null' EXIT
sleep 1
bin/collector$exe replay -sites collector/sim-sites.json
matcher/target/release/pricewise-match$exe "$@" run
(cd ml && "../$py" -m pricewise_ml.analyse)
(cd dashboard && npm install --no-audit --no-fund && npm run build && npm start)
