#!/usr/bin/env bash
# Instala o comando `nexus` no PATH e compila o harness.
#
# Rode uma vez:  ./install.sh
# Depois abra um terminal novo e digite:  nexus
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"

if ! command -v go >/dev/null 2>&1; then
  echo "erro: Go não encontrado no PATH." >&2
  echo "instale o Go 1.22+ (https://go.dev/dl/) e rode ./install.sh de novo." >&2
  exit 1
fi

echo ">> compilando o harness..."
(cd "$REPO" && go build -o harness .)

echo ">> instalando o comando em $BIN_DIR/nexus"
mkdir -p "$BIN_DIR"
cat >"$BIN_DIR/nexus" <<EOF
#!/usr/bin/env bash
# Gerado por install.sh — abre o Nexus a partir de $REPO
exec "$REPO/nexus.sh" "\$@"
EOF
chmod +x "$BIN_DIR/nexus"

# Garante que ~/.local/bin esteja no PATH dos próximos terminais.
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    line="export PATH=\"$BIN_DIR:\$PATH\""
    # Procura também a forma usada pelos dotfiles padrão do Ubuntu ("$HOME/.local/bin").
    if [ "$BIN_DIR" = "$HOME/.local/bin" ]; then
      pattern=".local/bin"
    else
      pattern="$BIN_DIR"
    fi
    updated=0
    for rc in "$HOME/.bashrc" "$HOME/.profile"; do
      [ -f "$rc" ] || continue
      if ! grep -qF "$pattern" "$rc"; then
        printf '\n# adicionado pelo install.sh do agentic-harness\n%s\n' "$line" >>"$rc"
        echo ">> PATH atualizado em $rc"
        updated=1
      fi
    done
    if [ "$updated" = 0 ]; then
      printf '\n# adicionado pelo install.sh do agentic-harness\n%s\n' "$line" >>"$HOME/.bashrc"
      echo ">> PATH atualizado em $HOME/.bashrc"
    fi
    ;;
esac

echo
echo "✅ pronto!"
echo "   Abra um terminal novo (ou rode: source ~/.bashrc) e digite: nexus"
echo "   O servidor do modelo sobe sozinho se não estiver no ar."
