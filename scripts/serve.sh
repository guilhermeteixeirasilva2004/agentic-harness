#!/usr/bin/env bash
# Sobe o llama-server. Por padrão usa a build CUDA e joga todas as camadas na GPU.
# A decodificação especulativa é opcional (defina DRAFT para ativar).
#
# Exemplos:
#   ./scripts/serve.sh                                   # GPU, tudo na VRAM
#   NGL=20 ./scripts/serve.sh                            # divide GPU (20 camadas) + CPU/RAM
#   BUILD=cpu NGL=0 ./scripts/serve.sh                   # tudo na CPU
#   DRAFT=~/llama.cpp/models/qwen2.5-0.5b-instruct-q4_k_m.gguf SPEC_NMAX=3 SPEC_TD=8 ./scripts/serve.sh
set -euo pipefail

LLAMA_DIR="${LLAMA_DIR:-$HOME/llama.cpp}"
BUILD="${BUILD:-cuda}"                       # cuda | cpu
MODEL="${MODEL:-$LLAMA_DIR/models/qwen2.5-3b-instruct-q4_k_m.gguf}"
CTX="${CTX:-8192}"
HOST="${HOST:-127.0.0.1}"
PORT="${PORT:-8080}"

NGL="${NGL:-99}"                             # camadas do modelo alvo na GPU
DRAFT="${DRAFT:-}"                           # caminho do modelo draft (opcional)
NGLD="${NGLD:-99}"                           # camadas do draft na GPU
SPEC_TYPE="${SPEC_TYPE:-draft-simple}"       # --spec-type (obrigatório p/ ligar a especulativa)
SPEC_NMAX="${SPEC_NMAX:-3}"                  # nº de tokens "rascunhados" por passo
SPEC_TD="${SPEC_TD:-}"                       # threads do draft (-td); vazio = padrão do llama.cpp
SPEC_TB="${SPEC_TB:-}"                       # threads de batch do alvo (-tb); vazio = padrão

# NOTA: a decodificação especulativa só compensa com amostragem gulosa
# (temperature=0). Com temperatura alta a taxa de aceitação do draft despenca e
# a especulativa fica MAIS LENTA que a geração normal. Ajuste no cliente.

BIN_DIR="$LLAMA_DIR/build-$BUILD/bin"
SERVER="$BIN_DIR/llama-server"

if [ ! -x "$SERVER" ]; then
  echo "erro: $SERVER não encontrado." >&2
  [ "$BUILD" = "cuda" ] && echo "dica: rode ./scripts/build-cuda.sh primeiro" >&2
  exit 1
fi
if [ ! -f "$MODEL" ]; then
  echo "erro: modelo não encontrado: $MODEL" >&2
  exit 1
fi

args=(-m "$MODEL" -c "$CTX" --host "$HOST" --port "$PORT")
[ -n "$SPEC_TB" ] && args+=(-tb "$SPEC_TB")

# Offload para a GPU (só faz sentido na build CUDA)
if [ "$BUILD" = "cuda" ]; then
  args+=(-ngl "$NGL")
fi

# Decodificação especulativa (opcional)
if [ -n "$DRAFT" ]; then
  if [ ! -f "$DRAFT" ]; then
    echo "erro: draft não encontrado: $DRAFT" >&2
    exit 1
  fi
  # --spec-type é obrigatório: o default é "none" (ou seja, NÃO liga sozinho)
  args+=(-md "$DRAFT" --spec-type "$SPEC_TYPE" --spec-draft-n-max "$SPEC_NMAX")
  # threads dedicadas ao draft (útil no CPU: o 0.5B escala bem até ~8 threads)
  [ -n "$SPEC_TD" ] && args+=(-td "$SPEC_TD")
  # camadas do draft na GPU (somente na build CUDA)
  if [ "$BUILD" = "cuda" ]; then
    args+=(-ngld "$NGLD")
  fi
fi

echo ">> servidor: $SERVER"
echo ">> flags   : ${args[*]}"
exec "$SERVER" "${args[@]}"
