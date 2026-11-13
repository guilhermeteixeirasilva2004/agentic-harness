#!/usr/bin/env bash
# depmap.sh — imprime o grafo de imports internos do módulo (A-01 / T-04).
#
# O mapa é a foto das arestas entre pacotes do próprio módulo: cada linha tem a
# forma "origem -> destino". Imports da stdlib e de terceiros ficam de fora.
#
# Uso:
#   scripts/depmap.sh          # uma aresta por linha (ordenado e sem repetição)
#   scripts/depmap.sh --dot    # mesmo grafo em Graphviz, para visualizar
#
# O resultado é usado em docs/dependency-map.md. Rode de novo sempre que criar,
# mover ou remover um pacote — o doc é a fonte de verdade da regra de dependência.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
module="$(go list -m)"
mode="${1:-text}"

mapa() {
	go list -f '{{.ImportPath}} {{join .Imports " "}}' ./... |
		awk -v mod="$module" '
			function norm(p) {
				if (p == mod) return "."
				sub("^" mod "/", "./", p)
				return p
			}
			{
				pkg = norm($1)
				for (i = 2; i <= NF; i++) {
					if ($i != mod && index($i, mod "/") != 1) continue
					printf "%s -> %s\n", pkg, norm($i)
				}
			}' | sort -u
}

case "$mode" in
--dot)
	echo "digraph imports {"
	echo "  rankdir=LR; node [shape=box, fontsize=10];"
	mapa | awk '{ printf "  \"%s\" -> \"%s\";\n", $1, $3 }'
	echo "}"
	;;
text | *)
	mapa
	;;
esac
