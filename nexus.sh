#!/usr/bin/env bash
# Launcher do "Nexus": abre o harness em modo agente com uma persona padrão.
#
# Uso:  nexus                        (depois de rodar ./install.sh uma vez)
#       ./nexus.sh [flags extras do `harness agent`]
#
# Exemplos:
#   nexus                                   # conversa normal
#   nexus -session nexus -resume            # retoma a conversa anterior
#   nexus -temp 0 -verbose                  # determinístico e mostrando decisões
#   nexus -shellallow "go,ls,cat"           # libera mais comandos de shell
#
# Variáveis de ambiente:
#   SYSTEM           sobrescreve o system prompt (persona)
#   ALLOW            sobrescreve a allowlist de tools
#   NEXUS_AUTOSERVE  =0 impede subir o servidor automaticamente
#   HARNESS_URL      endereço do servidor (padrão http://127.0.0.1:8080/v1)
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
START_DIR="$PWD"          # o agente trabalha no diretório de onde você chamou o `nexus`

cd "$REPO"

if [[ ! -x ./harness ]]; then
  echo "nexus: binário ./harness não encontrado; rode antes: ./install.sh" >&2
  exit 1
fi

# Sobe o modelo local automaticamente quando ele não está no ar.
# Desative com NEXUS_AUTOSERVE=0 (útil se o servidor estiver em outra máquina).
ensure_server() {
  local url="${HARNESS_URL:-http://127.0.0.1:8080/v1}"
  local hostport="${url#*://}"
  hostport="${hostport%%/*}"
  local host="${hostport%%:*}"
  local port="${hostport##*:}"
  [[ "$port" =~ ^[0-9]+$ ]] || port=8080
  [[ -n "$host" ]] || host=127.0.0.1

  server_up() { (exec 3<>"/dev/tcp/$host/$port") >/dev/null 2>&1; }

  if server_up; then
    return 0
  fi
  if [[ "${NEXUS_AUTOSERVE:-1}" == "0" ]]; then
    echo "nexus: nada escutando em $host:$port (NEXUS_AUTOSERVE=0)." >&2
    exit 1
  fi

  local log_dir="${XDG_STATE_HOME:-$HOME/.local/state}/agentic-harness"
  mkdir -p "$log_dir"
  echo "⏳ subindo o modelo local em $host:$port... (log: $log_dir/server.log)"
  nohup ./scripts/serve.sh >"$log_dir/server.log" 2>&1 &

  local i
  for i in $(seq 1 90); do
    if server_up; then
      echo "✅ modelo no ar."
      return 0
    fi
    sleep 1
  done
  echo "nexus: o modelo não respondeu em 90s; veja $log_dir/server.log" >&2
  exit 1
}

ensure_server

cd "$START_DIR"           # volta para onde você estava: o agente opera nesse diretório

SYSTEM="${SYSTEM:-Você é Nexus, o agente de programação do repositório agentic-harness. Responda em português do Brasil, de forma direta e objetiva; use as ferramentas para ler, editar e executar código antes de afirmar algo sobre o projeto. Quando a pergunta envolver data, hora, idade ou prazos, chame a ferramenta get_time para obter a data atual antes de responder.}"

ALLOW="${ALLOW:-get_time,read_file,list_dir,write_file,edit_file,run_shell,run_shell_bg,wait,remember,ask_user}"

exec "$REPO/harness" agent -system "$SYSTEM" -allow "$ALLOW" "$@"
