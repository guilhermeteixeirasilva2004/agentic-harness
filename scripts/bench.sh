#!/usr/bin/env bash
# Compara o desempenho do motor: CPU puro x GPU (-ngl 99) x GPU + especulativa.
# Requer as builds CPU (~/llama.cpp/build) e CUDA (~/llama.cpp/build-cuda).
# Imprime as linhas "Prompt:" / "Generation:" (tokens/s) de cada configuração.
# Usa amostragem gulosa (--temp 0), condição para a especulativa render de fato.
#
# IMPORTANTE: pare o llama-server antes de rodar (pkill -f llama-server).
# Caso contrário os testes disputam a mesma VRAM e podem falhar/empatar.
set -euo pipefail

if pgrep -f 'llama-server' >/dev/null 2>&1; then
  echo "aviso: há um llama-server rodando; pare-o antes (pkill -f llama-server) para medir corretamente." >&2
fi

LLAMA_DIR="${LLAMA_DIR:-$HOME/llama.cpp}"
MODEL="${MODEL:-$LLAMA_DIR/models/qwen2.5-3b-instruct-q4_k_m.gguf}"
DRAFT="${DRAFT:-$LLAMA_DIR/models/qwen2.5-0.5b-instruct-q4_k_m.gguf}"
PROMPT="${PROMPT:-Explique em um parágrafo o que é um buraco negro.}"
N="${N:-128}"
THREADS="${THREADS:-10}"

cli_cpu="$LLAMA_DIR/build/bin/llama-cli"
cli_gpu="$LLAMA_DIR/build-cuda/bin/llama-cli"

run() {
  local label="$1"
  shift
  local bin="$1"
  shift
  echo "=== $label ==="
  if [ ! -x "$bin" ]; then
    echo "(binário ausente: $bin)"
    return
  fi
  # --temp 0 (guloso): sem isso a especulativa perde por baixa aceitação do draft
  "$bin" -m "$MODEL" "$@" -p "$PROMPT" -n "$N" -t "$THREADS" --temp 0 \
    -st --no-display-prompt </dev/null 2>&1 | grep -aE 'Prompt:|Generation:' || true
}

run "CPU puro"                      "$cli_cpu"
run "CPU + especulativa (0.5B, n=3, td=8)" "$cli_cpu" -c 2048 -md "$DRAFT" --spec-type draft-simple --spec-draft-n-max 3 -td 8
run "CPU + especulativa (0.5B, n=4, td=8)" "$cli_cpu" -c 2048 -md "$DRAFT" --spec-type draft-simple --spec-draft-n-max 4 -td 8
run "GPU (-ngl 99)"                 "$cli_gpu" -ngl 99 -c 4096
run "GPU + especulativa (n-max 4)"  "$cli_gpu" -ngl 99 -c 2048 -md "$DRAFT" -ngld 99 --spec-type draft-simple --spec-draft-n-max 4
run "GPU + especulativa (n-max 12)" "$cli_gpu" -ngl 99 -c 2048 -md "$DRAFT" -ngld 99 --spec-type draft-simple --spec-draft-n-max 12
