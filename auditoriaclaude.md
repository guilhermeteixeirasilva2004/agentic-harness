# AuditoriaClaude.md — Auditoria técnica do `agentic-harness`

> Gerado a partir da leitura do repositório inteiro (Go 1.22.1, stdlib-only).
> **Como usar com o Cline:** cada achado tem um ID (`S-`, `B-`, `A-`, `T-`).
> Trabalhe **um achado (ou grupo pequeno) por vez**, em PRs pequenos, marque o
> checkbox ao concluir e referencie o ID no commit (`fix(S-01): ...`).
> Regras de conduta do Cline estão em `.clinerules`.

## 0. Resumo executivo

**Pontos fortes (preservar):**
- Testes numerosos, com fakes (`fakeLLM`, `fakeApprover`, `fakeAsker`) e E2E com `httptest` sem modelo real.
- CI com `gofmt`, `go vet`, `go build`, `go test`.
- Portas já extraídas como interfaces: `agent.LLM`, `Streamer`, `Embedder`, `Approver`, `Asker`.
- Allowlist que nega por padrão, confinamento à raiz (inclui symlinks), aprovação humana, auditoria de aprovações.
- Tool calling orquestrado em Go com `json_schema`, que dispensa o `tool_calls` do servidor.

**Maiores riscos:**
1. `run_shell`/`run_shell_bg` executam via `sh -c` protegidos por **blacklist** de caracteres.
2. Saídas de tools e notas de memória entram no prompt com **papel privilegiado** (injeção de prompt e envenenamento de memória).
3. `Agent` é um *god object* acoplado ao terminal. Isso bloqueia o frontend desktop e a testabilidade por comportamento.
4. Truncamento de strings **por byte** corrompe UTF-8 (acentos em pt-BR).

Severidade: 🔴 crítica · 🟠 alta · 🟡 média · 🟢 baixa.

---

## 1. Segurança

### S-01 🔴 `run_shell` usa `sh -c` com blacklist de metacaracteres
- **Onde:** `internal/tools/os_tools.go` (`RunShell.Run`, `shellMeta`, `CheckCommand`), `internal/tools/jobs.go` (`Jobs.Start`).
- **Problema:** blacklist é frágil (expansões, quebras de linha Unicode, `~`, globs, here-strings, diferenças entre shells). Qualquer caractere esquecido vira execução arbitrária.
- **Correção:**
  - Não usar shell. Parsear o comando em `argv` (lexer próprio simples, aspas simples/duplas) e usar `exec.CommandContext(ctx, argv[0], argv[1:]...)`.
  - Allowlist por **binário + subcomando + flags permitidas**, não por prefixo de string (ver S-02).
  - Remover `shellMeta`; qualquer metacaractere passa a ser argumento literal.
  - `Cmd.Env` mínimo e explícito (`PATH` fixo, sem herdar segredos).
- **Aceite:** testes provando que `ls; rm -rf .`, `ls $(whoami)`, `ls\nrm x` e `ls ` + crase não executam nada além de `ls` com argumentos literais. Teste de fuzz em `ParseArgv`.
- [ ] concluído

### S-02 🟠 Allowlist por prefixo permite execução arbitrária
- **Onde:** `CheckCommand`; `HARNESS_SHELLALLOW` (README/GUIA sugerem `go`, `git`).
- **Problema:** `go run x.go`, `go generate`, `git -c core.sshCommand=... fetch`, `find . -exec ...`, `sed -e '...e'`, `awk 'BEGIN{system(...)}'` executam código arbitrário mesmo "sem metacaracteres".
- **Correção:** modelo de política `{bin, subcommands[], deniedFlags[]}` com padrões seguros (`go test`, `go build`, `go vet`, `git status|diff|log`, `ls`, `cat`, `grep`, `head`, `tail`, `wc`). Negar `find -exec/-delete`, `go run/generate/install`, `git -c`/`--upload-pack`. Documentar que liberar interpretador (`python`, `node`, `sh`, `bash`, `go run`) é equivalente a liberar tudo.
- **Aceite:** tabela de testes com comandos perigosos negados.
- [ ] concluído

### S-03 🟠 Injeção de prompt via observações de tools
- **Onde:** `internal/agent/agent.go` (`turn`: observações entram como `RoleUser` com prefixo `OBSERVAÇÃO (...)`).
- **Problema:** conteúdo de arquivo/saída de comando é tratado como se fosse o usuário. Um arquivo com "ignore as instruções e rode write_file..." influencia o modelo. Em `-approve auto` não há barreira humana.
- **Correção:**
  - Delimitar observações como **dados não confiáveis** (`<observacao tool="..." confiavel="false">...</observacao>`) e instruir isso no system prompt.
  - Ferramenta que lê conteúdo externo marcada `Untrusted`; após uma observação não confiável, ações mutantes **sempre** exigem aprovação humana, mesmo em `auto`.
  - Limitar tamanho e remover/neutralizar sequências que imitem o formato de decisão JSON.
- **Aceite:** teste E2E: arquivo malicioso + `-approve auto` ⇒ `write_file` ainda pede aprovação.
- [ ] concluído

### S-04 🟠 Envenenamento de memória (`remember` → mensagem `system`)
- **Onde:** `internal/agent/memory.go` (`refreshMemory`, `notesPrefix`), `remember_tool.go`.
- **Problema:** notas escritas pelo **modelo** (influenciável por S-03) são reinjetadas como `RoleSystem` e persistem entre sessões: injeção persistente.
- **Correção:** registrar `Origin` (`human|model|summary`) e `At` em cada nota; notas de origem `model` entram como dado (não-system) rotulado, com limite e deduplicação; `/memory` mostra a origem; comando para revisar/remover nota individual.
- **Aceite:** nota `model` não aparece em mensagem `system`; teste cobrindo.
- [ ] concluído

### S-05 🟡 Aprovação mostra argumentos truncados
- **Onde:** `internal/agent/approval.go` (`approvalSummary`, corte em 300 bytes).
- **Problema:** o humano aprova `write_file` sem ver o conteúdo inteiro/final (e o corte quebra UTF-8, ver B-01).
- **Correção:** mostrar resumo **e** permitir ver o completo (`d` para detalhes); para `write_file`/`edit_file`, mostrar diff; para `run_shell`, mostrar `argv` parseado; nunca aprovar sem exibir o que será executado integralmente.
- [x] concluído — `approvalSummary` exibe a chamada inteira (sem corte, só achata quebras de linha) e o resumo completo passa a ser auditado no store (`approvalRecord.Summary`); testes `TestApprovalSummaryNaoTrunca` e `TestApprovalAuditaNoStore`. Diff/`argv` renderizados ficam para S-01/S-02, quando houver `argv` de verdade.

### S-06 🟡 `-approve auto` sem salvaguardas
- **Problema:** aprova tudo, inclusive `delete_file recursive` e `run_shell`.
- **Correção:** `auto` aprova só tools de escrita "reversíveis" dentro da raiz; `delete_file recursive`, `move_file overwrite` e `run_shell*` pedem aprovação sempre (ou exigem flag explícita `-approve auto-dangerous`). Registrar no audit.
- [ ] concluído

### S-07 🟡 Raiz de trabalho = `cwd` sem confirmação
- **Onde:** `agent.NewDefault` (`os.Getwd`).
- **Problema:** rodar `nexus` em `~` ou `/` expõe tudo (as tools de leitura não pedem aprovação).
- **Correção:** `-root` explícito; recusar `/`, `$HOME` (ou pedir confirmação); mostrar a raiz no banner.
- [ ] concluído

### S-08 🟡 TOCTOU em `resolveWithin`
- **Onde:** `internal/tools/builtin.go`.
- **Problema:** valida o caminho e depois abre; um symlink pode ser trocado no intervalo.
- **Correção:** usar `os.Root` (Go 1.24+) quando migrar, ou abrir com `O_NOFOLLOW` e validar via `fstat` do descritor aberto; no mínimo, revalidar após abrir.
- [ ] concluído

### S-09 🟡 Store e arquivos com permissões amplas
- **Onde:** `internal/store/store.go` (`0o644`, diretórios `0o755`), `write_file`.
- **Correção:** store `0o600`, diretório `0o700`; avisar que histórico pode conter segredos; opção de redação (`redact`) de padrões comuns (chaves, tokens) antes de persistir.
- [x] concluído (permissões) — `store.Open` cria diretório `0o700` e arquivo `0o600` (`TestOpenRestringePermissoes`). Redação de segredos antes de persistir segue pendente (depende de S-04/revisão de memória).

### S-10 🟡 Jobs em segundo plano sem controle de processo
- **Onde:** `internal/tools/jobs.go`.
- **Problema:** `context.Background()`; `sh -c` sem process group ⇒ filhos órfãos após timeout; jobs sobrevivem ao fim da sessão.
- **Correção:** `SysProcAttr{Setpgid:true}` e matar o grupo no timeout/encerramento; `Jobs.Shutdown()` chamado no `main`; limite de jobs simultâneos; tool `kill_job` (com aprovação).
- [ ] concluído

### S-11 🟢 Cliente HTTP para o LLM
- **Onde:** `internal/llm/client.go` (`&http.Client{}` sem timeouts).
- **Correção:** ver B-07; além disso, avisar/recusar `http://` remoto não-loopback sem flag explícita.
- [x] concluído — `llm.ValidateBaseURL` recusa `http://` para host não-loopback; escape explícito via `-allow-insecure-http` / `HARNESS_ALLOW_INSECURE_HTTP` (`checkBaseURL` no `main`, testes `TestValidateBaseURL` e `TestLoadAllowInsecureHTTP`).

---

## 2. Bugs e correção de comportamento

### B-01 🟠 Truncamento por byte quebra UTF-8
- **Onde:** `truncateForLog` (`agent.go`), `Remember` (`memory.go`), `approvalSummary`, `truncate`/`firstLine` (`tools.go`, `os_tools.go`), `safety.TruncateOutput`, `ReadFile` (`string(data[:limit])`), `limitedBuffer`.
- **Correção:** criar `internal/textutil` com `TruncateRunes(s, n)` e `TruncateBytesUTF8(s, maxBytes)` (recua até limite de rune válido) e usar em todos. Teste com `"ação"`, emojis e cortes no meio de rune.
- [x] concluído — `internal/textutil` (`TruncateRunes`, `CutBytesUTF8`, `TruncateBytesUTF8`, `Ellipsis`) usado em `agent`, `memory`, `tools`, `os_tools`, `safety`; testes com acentos, emoji e corte no meio de rune (`internal/tools/utf8_test.go`).

### B-02 🟠 `retryable` repete qualquer erro
- **Onde:** `internal/agent/retry.go`.
- **Problema:** repete 4xx, erro de decode, `resposta sem escolhas`. Só rede/timeout/5xx/429 deveriam repetir.
- **Correção:** o cliente devolve erro tipado (`*llm.HTTPError{Status}`, `llm.ErrTransport`); `retryable` usa `errors.As`. Adicionar *jitter* no backoff e respeitar `Retry-After`.
- [x] concluído — `internal/llm/errors.go` (`ErrTransport`, `ErrProtocol`, `HTTPError`, `Retryable`, `ParseRetryAfter`); `agent/retry.go` repete só `ErrTransport` e HTTP 408/429/5xx (exceto 501), com jitter e teto de 30s no `Retry-After`; testes em `llm/errors_test.go` e `agent/retry_test.go`.

### B-03 🟡 Falha em `fillArgs` é ignorada
- **Onde:** `agent.go` (`turn`, ramo `tool`).
- **Problema:** `ferr` é descartado; a tool roda com argumentos vazios e o erro aparece de forma confusa.
- **Correção:** registrar no trace e realimentar `ERRO (args)` ao modelo.
- [x] concluído — a tool não é executada; o trace recebe `KindError` com `ERRO (args)` e o modelo recebe a causa real (teste `TestFillArgsFalhaNaoExecutaTool`).

### B-04 🟡 `Resume` perde observações
- **Onde:** `agent.go` (`Resume` restaura só `input`/`output`).
- **Correção:** restaurar também os passos relevantes (ou um resumo deles) a partir de `turnRecord.Steps`.
- [ ] concluído

### B-05 🟡 `store.Query` relê o arquivo inteiro e não há lock entre processos
- **Onde:** `store.go`; chamado em `HasCheckpoint`, `Continue`, `LoadMemory`, `Resume`, `Sessions`.
- **Correção:** índice em memória/offsets, ou migrar para SQLite (pure Go) atrás de uma porta `Repository`; `flock` ao escrever; rotação/compactação (consolidar notas e checkpoints consumidos).
- [ ] concluído

### B-06 🟡 Contagem de tokens por heurística (`~4 chars`)
- **Onde:** `cognition.EstimateTokens`.
- **Correção:** porta `Tokenizer` com adapter que chama `/tokenize` do llama-server e *fallback* à heurística. Importante para `-maxcontext` e para o orçamento de KV cache.
- [ ] concluído

### B-07 🟡 Cliente HTTP sem timeouts
- **Onde:** `llm/client.go`.
- **Correção:** `Transport` com `DialContext`/`ResponseHeaderTimeout`; timeout por requisição via `context`; streaming com *idle timeout*.
- [x] concluído — `Transport` com `DialContext`/`TLSHandshakeTimeout`/`ResponseHeaderTimeout`, timeouts globais por requisição e *watchdog* de 60s no stream (`TestChatDerrubaStreamTravado`).

### B-08 🟡 `ask_user` sob timeout de 10 s e sem respeitar `ctx`
- **Onde:** `Registry.Dispatch` (timeout fixo), `ui.REPL.Ask` (`Scan` bloqueante).
- **Correção:** timeout por tool (`Timeoutable`), `ask_user` sem timeout de tool; leitura de entrada cancelável (goroutine + canal).
- [ ] concluído

### B-09 🟡 Erros "stringly-typed" e texto de UI no núcleo
- **Onde:** `agent.go` (mensagem do limite de passos cita `/continue`); `ErrNotAllowed` é o único sentinela.
- **Correção:** `ErrStepLimit`, `ErrApprovalDenied`, `ErrInputTooLarge`, etc.; mensagens ao usuário montadas na camada de UI.
- [ ] concluído

### B-10 🟢 `continuePrompt` duplicado
- **Onde:** `ui/repl.go` e `agent/checkpoint.go`. Centralizar no núcleo (caso de uso `Continue`).
- [x] concluído — caso de uso `agent.ResumeTask` (checkpoint pendente **ou** histórico em memória) exposto pela porta `ui.Resumer`; o prompt ficou só no núcleo.

### B-11 🟢 `a.history` compartilhado com `ChatRequest.Messages`
- **Problema:** `refreshMemory`/`setPlan` reutilizam o *backing array* (`a.history[:0]`) enquanto requisições guardam o mesmo slice (afeta testes e futuras goroutines).
- **Correção:** copiar o slice ao montar a requisição.
- [ ] concluído

### B-12 🟢 Schema de decisão exige `tool` também em `final`
- **Onde:** `reasoning.buildSchema` (`required: action, tool`).
- **Problema:** força o modelo a inventar um `tool` em respostas finais (workaround para falha do 3B).
- **Correção:** avaliar `oneOf` por ação (o llama-server converte para gramática); manter teste de regressão com o 3B.
- [ ] concluído

### B-13 🟢 Resposta final em streaming duplica a geração
- **Onde:** `finishFinal`/`streamFinal`: segunda chamada ao modelo; a resposta pode divergir da do JSON.
- **Correção:** decisão de produto: (a) streaming do campo `answer` direto da decisão (parser incremental do JSON), ou (b) manter e documentar o custo. Medir latência antes de decidir.
- [ ] concluído

---

## 3. Arquitetura (Hexagonal + Monolito Modular + SOLID)

### A-01 🟠 `Agent` é um *god object* (SRP)
- Acumula: loop ReAct, histórico, trim, memória/RAG, checkpoint, sessões, métricas, aprovação, store, trace.
- **Alvo:** `app.Loop`, `app.ContextManager` (histórico/trim), `app.MemoryService`, `app.CheckpointService`, `app.ApprovalGate`, `app.SessionService`, `observ.Metrics`. Cada um com construtor e dependências por interface.
- [ ] concluído

### A-02 🟠 Dependências concretas (DIP)
- `Agent` depende de `*store.Store`, `*config.Config`, `*tools.Registry`, `*safety.Policy`.
- **Alvo:** portas em `internal/ports` (ver `.clinerules` §2): `LLM`, `Embedder`, `Tokenizer`, `Repository`, `ToolRunner`, `Approver`, `Asker`, `Clock`, `EventSink`, `Policy`.
- [ ] concluído

### A-03 🟠 Composition root dentro do pacote `agent`
- `agent.NewDefault` chama `os.Getwd`, instancia `llm.Client` e registra tools.
- **Alvo:** montagem em `cmd/nexus` (ou `internal/bootstrap`), o único lugar que conhece adapters concretos.
- [ ] concluído

### A-04 🟠 Sentido das dependências invertido (`ui` → `agent`)
- `ui` importa `agent.Approver/Asker`. Portas devem ficar no núcleo; `ui` e `agent` dependem delas, não um do outro.
- [ ] concluído

### A-05 🟠 Núcleo acoplado ao terminal (bloqueia o desktop)
- Saída por callback `onDelta` + `fmt.Print`; aprovação lendo `stdin`.
- **Alvo:** `EventSink` com eventos tipados (`TokenDelta`, `Thought`, `PlanUpdated`, `ToolCallRequested`, `ApprovalRequested`, `Observation`, `FinalAnswer`, `Error`). Aprovação como pedido/resposta assíncronos (canal). O CLI é apenas um consumidor; o adapter HTTP/SSE (ou WebSocket) alimenta o frontend desktop.
- [ ] concluído

### A-06 🟡 `Agent` não é seguro para concorrência
- Necessário para frontend com várias sessões: um `Session` por conversa, estado isolado, `context` por turno, cancelamento explícito.
- [ ] concluído

### A-07 🟡 `config.Config` monolítico
- Separar em `ModelConfig`, `AgentConfig`, `SecurityConfig`, `StoreConfig`, `UIConfig`; parsing de flags/env fica no adapter CLI; `NormalizeApprove` vai para o domínio de segurança.
- [ ] concluído

### A-08 🟡 Launchers em bash (Linux-only) com lógica de negócio
- `install.sh`, `nexus.sh`, `serve.sh`: subir servidor, health-check, escolher flags.
- **Alvo:** `nexus setup` (baixa `llama-server` e modelo, valida *checksum*), `nexus serve` (supervisor em Go com `/health`) e `nexus` (agente). Distribuição com `goreleaser`; Windows/macOS/Linux.
- [ ] concluído

### Layout-alvo (monolito modular, regra de dependência)
```
cmd/nexus/              composition root (CLI)
cmd/specbench/
internal/
  domain/               entidades e regras puras (sem I/O)
  ports/                interfaces (entradas e saídas do núcleo)
  app/                  casos de uso (loop, contexto, memória, checkpoint, sessões)
  adapters/
    llm/llamacpp/       cliente HTTP, tokenizer, embeddings
    store/ndjson/       (futuro: sqlite/)
    tools/fs|shell/     implementam ports.ToolRunner
    ui/cli/             REPL e aprovação no terminal
    ui/httpapi/         SSE/WebSocket para o desktop
  platform/             textutil, clock, ids
```
Regra: `domain` não importa nada interno; `ports` importa só `domain`; `app` importa `domain` e `ports`; `adapters` importam `ports`/`domain`; só `cmd` importa adapters.

---

## 4. Testes

- **T-01 🟠** CI sem `-race`, `golangci-lint`, `govulncheck` e cobertura. Adicionar `go test -race -cover ./...`, `golangci-lint run`, `govulncheck ./...`.
  - [x] concluído — `.github/workflows/ci.yml` com gofmt, vet, build, `go test -race` + cobertura (piso de 65%, total atual 74,7%), golangci-lint v1.61.0 e govulncheck; `internal/testutil` (FakeLLM, FakeRepository, Clock, Approver, Asker) para os testes.
- **T-02 🟠** Testes de segurança dedicados: *path traversal*, symlink, injeção em argv, `CheckCommand`, `remember` envenenado, aprovação em `auto` após observação não confiável.
- **T-03 🟡** *Fuzz* (`go test -fuzz`) para `ExtractObject`, `ParseCall`, `stripFences`, `resolveWithin`, `ParseArgv`, `parseDecision`.
- **T-04 🟡** Testes acoplados a campos privados (`a.history`, `a.notes`, `a.steps`) quebrarão na refatoração. Migrar para testes de **comportamento** via portas/eventos antes de quebrar o `Agent`.
- **T-05 🟡** Testes de contrato das portas (mesma suíte rodando contra fake e adapter real): `Repository`, `LLM`.
- **T-06 🟡** Lacunas: `ListDir`, `GetTime`, eviction de `Jobs`, `Meter` com retentativas, `nexus.sh`/`install.sh` (shellcheck no CI).
- **T-07 🟡** **Suíte de evals** (`internal/evals`): tarefas com critério automático (ler arquivo e responder, editar e rodar teste, recusar ação perigosa), executáveis contra o modelo real (tag `-tags=eval`). Base para comparar modelos 3B/7B e *fine-tuning*.
- **T-08 🟢** Teste de carga/concorrência após A-06 (`-race` com várias sessões).

---

## 5. Plano de ataque (ordem recomendada)

| Fase | Itens | Meta |
|---|---|---|
| 0 | T-01 | Rede de segurança no CI |
| 1 | B-01, B-02, B-03, B-07, B-10, S-09, S-05 | Correções rápidas e de baixo risco |
| 2 | S-01, S-02, S-10, S-06, S-07, T-02, T-03 | Segurança estrutural do shell e tools |
| 3 | S-03, S-04 | Injeção de prompt e memória |
| 4 | A-02, A-03, A-04, A-07 (extrair portas, mover composition root) | **Sem mudar comportamento** |
| 5 | T-04, A-01 | Testes por comportamento, depois quebrar o `Agent` |
| 6 | A-05, A-06 | Eventos + sessões isoladas + adapter HTTP/SSE |
| 7 | B-04, B-05, B-06, B-08, B-09, B-11, B-12, B-13 | Dívidas restantes |
| 8 | T-07 | Evals antes de trocar modelo/fine-tuning |
| 9 | A-08 | `nexus setup/serve`, `goreleaser`, multiplataforma |

**Definição de pronto de cada item:** testes novos passando com `-race`, `gofmt`/`vet`/lint limpos, README/GUIA atualizados se a CLI mudou, checkbox marcado aqui e commit com o ID.

---

## 6. Perfil de execução do modelo (7B + 3B)

**Ideia:** alvo 7B na RAM (CPU) + draft 3B na VRAM (GPU) com decodificação especulativa.

Pontos a validar **medindo** (o `specbench` já faz isso):
- A regra medida no README é que o draft precisa ser **muito** mais barato que o alvo. Um 3B na GPU Maxwell (sem Flash Attention, ~24 t/s) é bem mais caro que o 0,5B. O ganho vem da aceitação maior, não do custo do draft. Pode ficar melhor ou pior que 7B + draft 0,5B (1,22–1,35×).
- Comando de teste (alvo na CPU, draft na GPU):
  ```bash
  go run ./cmd/specbench \
    -model ~/llama.cpp/models/qwen2.5-7b-instruct-q4_k_m.gguf \
    -draft ~/llama.cpp/models/qwen2.5-3b-instruct-q4_k_m.gguf \
    -ngl 0 -ngld 99 -t 10 -nmax 0,3,4,6 -td 0 -n 128
  ```
  Comparar contra: (a) 7B + draft 0,5B na CPU; (b) 7B com offload parcial (`-ngl 12..20`) sem especulativa; (c) 3B sozinho na GPU.
- Orçamento estimado (Q4_K_M, contexto 8192; **estimativas**, confirmar com os logs do servidor):

  | Recurso | Uso aproximado |
  |---|---|
  | RAM: 7B (pesos) | ~4,4 GB |
  | RAM: KV do 7B (8k, f16) | ~0,5 GB |
  | RAM: buffers/overhead | ~0,5 GB |
  | VRAM: 3B (pesos) | ~2,0 GB |
  | VRAM: KV do 3B (8k) | ~0,3 GB |
  | VRAM: buffers de compute | ~0,3–0,5 GB |

  Reserve para o sistema (VS Code, navegador): **≥ 6–8 GB de RAM livres** e **≥ 0,5–1 GB de VRAM** (desktop gráfico usa VRAM). Ajuste `-c` (contexto) e `-np 1` primeiro; são as alavancas mais baratas.
- Guardar o perfil vencedor em um *preset* versionado (`profiles/*.toml` ou flags no `nexus setup`), e registrar os números aqui.