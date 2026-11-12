#!/usr/bin/env bash
# Recompila o llama.cpp com backend CUDA (em build-cuda/, sem mexer no build CPU).
set -euo pipefail

LLAMA_DIR="${LLAMA_DIR:-$HOME/llama.cpp}"
# 52 = Maxwell (GTX 960/970/980); 61 = Pascal; 75 = Turing; 86/89 = Ampere/Ada.
CUDA_ARCH="${CUDA_ARCH:-52}"
JOBS="${JOBS:-10}"

if [ ! -d "$LLAMA_DIR" ]; then
  echo "erro: LLAMA_DIR não existe: $LLAMA_DIR" >&2
  exit 1
fi

cd "$LLAMA_DIR"

echo ">> configurando build-cuda (arch=$CUDA_ARCH)"
cmake -B build-cuda \
  -DGGML_CUDA=ON \
  -DCMAKE_CUDA_ARCHITECTURES="$CUDA_ARCH" \
  -DCMAKE_BUILD_TYPE=Release

echo ">> compilando (-j $JOBS)"
cmake --build build-cuda -j "$JOBS" \
  --target llama-server llama-cli llama-bench

echo ">> pronto: $LLAMA_DIR/build-cuda/bin"
