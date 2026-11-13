# Mapa de dependências internas

Fotografia das arestas de import **entre pacotes do próprio módulo** no fim da
Fase A (base `71b0034` + suíte de evals), atualizada a cada extração do passo 2.
É o artefato do passo 1 do refactor (A-01/A-03/A-04) e a referência para o teste
automatizado da regra de dependência. Imports de stdlib ficam de fora de
propósito: a regra que nos interessa é "core não conhece adapter".

Regenerar depois de criar, mover ou remover um pacote:

```sh
scripts/depmap.sh          # origem -> destino, ordenado
scripts/depmap.sh --dot    # mesmo grafo em Graphviz
```

Pacotes atrás de build tag não entram no mapa padrão. `internal/evals` aparece
mesmo assim porque **não importa nenhum pacote interno** (é catálogo puro); para
incluir `cmd/evals` (tag `eval`), rode `GOFLAGS=-tags=eval scripts/depmap.sh`.

## Arestas atuais

```text
. -> ./internal/agent
. -> ./internal/config
. -> ./internal/llm
. -> ./internal/store
. -> ./internal/ui
./cmd/specbench -> ./internal/specbench
./internal/agent -> ./internal/checkpoint
./internal/agent -> ./internal/cognition
./internal/agent -> ./internal/config
./internal/agent -> ./internal/llm
./internal/agent -> ./internal/observ
./internal/agent -> ./internal/present
./internal/agent -> ./internal/reasoning
./internal/agent -> ./internal/safety
./internal/agent -> ./internal/store
./internal/agent -> ./internal/textutil
./internal/agent -> ./internal/tools
./internal/checkpoint -> ./internal/llm
./internal/checkpoint -> ./internal/store
./internal/llm -> ./internal/textutil
./internal/present -> ./internal/llm
./internal/present -> ./internal/store
./internal/present -> ./internal/textutil
./internal/safety -> ./internal/textutil
./internal/testutil -> ./internal/llm
./internal/testutil -> ./internal/store
./internal/tools -> ./internal/textutil
./internal/ui -> ./internal/agent
./internal/ui -> ./internal/config
./internal/ui -> ./internal/llm
```

Com a tag `eval`, soma-se o composition root do runner (mantido fora do mapa
padrão para não depender de build tag):

```text
./cmd/evals -> ./internal/agent
./cmd/evals -> ./internal/config
./cmd/evals -> ./internal/evals
./cmd/evals -> ./internal/llm
./cmd/evals -> ./internal/store
```

## Tamanho dos pacotes (linhas de `.go`, incluindo testes)

| Pacote | Linhas | Papel atual |
|---|---:|---|
| `internal/agent` | 4182 | loop ReAct + contexto + memória + checkpoint + sessões + aprovação + composição |
| `internal/tools` | 1900 | tools e registry |
| `internal/ui` | 791 | REPL, aprovação e renderização no terminal |
| `internal/llm` | 689 | cliente OpenAI-compatible |
| `internal/testutil` | 607 | fakes de teste |
| `internal/config` | 443 | flags e variáveis `HARNESS_*` |
| `internal/specbench` | 430 | harness de benchmark |
| `internal/evals` | 581 | catálogo de 12 tarefas, graders e runner NDJSON (zero imports internos) |
| `internal/cognition` | 350 | trace de passos |
| `internal/observ` | 288 | métricas |
| `internal/store` | 277 | NDJSON append-only |
| `internal/reasoning` | 277 | estratégias react/cot/plan |
| `internal/present` | 233 | formatação de histórico/sessões (sem I/O e sem `agent`) |
| `internal/checkpoint` | 186 | detecção do checkpoint pendente (save state), puro sobre `store.Record` |
| `cmd/evals` | 228 | composition root do runner de evals (tag `eval`) |
| `internal/safety` | 153 | política de passos/allowlist/truncamento |
| `internal/textutil` | 129 | corte de UTF-8 |
| `internal/arch` | 79 | teste de arquitetura (anti-ciclo) via `go list` |

## Violações da regra de dependência

| # | Aresta | ID | Por quê | Correção planejada |
|---|---|---|---|---|
| 1 | `ui -> agent` | A-04 | A camada de apresentação define o contrato do núcleo. Pior: `ui` importa `agent` só para `ApprovalRequest`, `Approver` e `Asker`, e o `Agent` importa de volta conceitos de UI (mensagens citam `/continue`). | Mover as portas `Approver`/`Asker`/`ApprovalRequest` para `internal/ports` e fazer `ui` depender só delas (passo 3). |
| 2 | `agent -> config` | A-03 | O núcleo lê a struct de flags da CLI; o composition root (`NewDefault`) mora dentro do núcleo e chama `os.Getwd`, monta registry, cliente e política. | `agent.New` recebe dependências injetadas; `NewDefault` sai para `cmd/`/`internal/bootstrap` (passo 3). |
| 3 | `agent -> tools/store/llm/safety/...` | A-01 | Nove pacotes internos entram no `Agent`: loop, contexto, memória, checkpoint, sessões, aprovação e métricas no mesmo tipo. | Extrair `present` (2a, **feito**), `checkpoint` (2b, **feito**) e memória (2c); portas no passo 3. |

## Arestas que o teste de arquitetura deve proibir

Estas são as regras do layout-alvo (`clinerules.md` §2), na ordem em que serão
aplicadas — cada linha vira um caso de teste em `internal/arch`:

1. `domain` não importa nenhum pacote interno e não usa `net/http`, `os/exec`
   nem `fmt` para I/O.
2. `ports` importa apenas `domain`.
3. `app` importa `domain` e `ports`; **nunca** `adapters`.
4. `adapters` importam `ports`/`domain`; **adapter não importa adapter**.
5. Só `cmd/*` importa adapters.

Enquanto o layout antigo existir, o teste roda em modo "dívida": as arestas
acima são listadas como exceções explícitas (com o ID) e a lista só pode
encolher. Assim a regra é verificável desde o primeiro commit sem exigir a
migração completa.
