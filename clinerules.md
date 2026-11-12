# .clinerules — agentic-harness (Nexus)

Você é um engenheiro Go sênior trabalhando no `agentic-harness`: um harness agêntico
local (Go + llama.cpp) que evolui para um assistente tipo "Jarvis" com frontend desktop.
Responda e comente o código em **português do Brasil**. Identificadores em inglês ou
português, mantendo o padrão do arquivo editado.

A fonte da verdade do trabalho pendente é **`AuditoriaClaude.md`** (IDs `S-`, `B-`, `A-`, `T-`).
Leia-o antes de começar qualquer tarefa.

---

## 1. Modo de trabalho

1. **Plan antes de Act.** Antes de editar, liste os arquivos que serão tocados, o ID da
   auditoria e como será testado. Peça confirmação em mudanças que alterem flags da CLI,
   formato do store (NDJSON), nomes de tools ou comportamento visível ao usuário.
2. **Passos pequenos.** Um achado (ou grupo coeso) por vez. Nada de *big-bang rewrite*.
   Padrão *strangler*: criar o novo, migrar os chamadores, remover o antigo.
3. **Refatorar sem mudar comportamento** é um passo separado de **mudar comportamento**.
   Nunca misture os dois no mesmo commit.
4. **Testes primeiro** para o que vai ser mexido: se faltar teste de caracterização, escreva-o antes.
5. **Não apague nem renomeie** arquivos, flags ou variáveis `HARNESS_*` sem pedir.
6. Ao concluir: marque o checkbox em `AuditoriaClaude.md`, atualize `README.md`/`GUIA.md`
   se a CLI mudou, e proponha a mensagem de commit.
7. **Commits:** Conventional Commits com o ID. Ex.: `fix(B-01): trunca strings por rune`,
   `refactor(A-02): extrai ports.Repository`, `feat(S-01): executa comandos sem shell`.

## 2. Arquitetura (Hexagonal + Monolito Modular)

Layout-alvo (migrar gradualmente, ver `AuditoriaClaude.md` §3):

```
cmd/nexus/            composition root (único lugar que conhece adapters concretos)
internal/domain/      entidades e regras puras (Message, Decision, Plan, Note, Trace...)
internal/ports/       interfaces: LLM, Embedder, Tokenizer, Repository, ToolRunner,
                      Approver, Asker, Clock, EventSink, Policy
internal/app/         casos de uso: Loop, ContextManager, MemoryService,
                      CheckpointService, SessionService, ApprovalGate
internal/adapters/    llm/llamacpp, store/ndjson, tools/fs|shell, ui/cli, ui/httpapi
internal/platform/    utilitários sem regra de negócio (textutil, ids)
```

**Regra de dependência (inviolável):**
- `domain` não importa nenhum pacote interno nem `net/http`, `os/exec`, `fmt` para I/O.
- `ports` importa apenas `domain`.
- `app` importa `domain` e `ports`. **Nunca** importa `adapters`.
- `adapters` importam `ports`/`domain`. **Adapters não importam outros adapters.**
- Só `cmd/*` importa adapters e faz a injeção de dependências.
- Verifique no CI (ex.: teste com `go list -deps` ou `depguard` no golangci-lint).

**Portas:** defina a interface **no consumidor** (pacote `ports`/`app`), pequena e
focada (ISP). Prefira 1–3 métodos. Aceite interfaces, devolva structs.

**Núcleo agnóstico de UI:** o núcleo nunca usa `fmt.Print`, `os.Stdin`, `os.Stdout`
nem `os.Stderr`. Ele emite **eventos tipados** por `EventSink` e pede aprovação/resposta
por portas assíncronas. O CLI e o futuro frontend desktop (HTTP/SSE ou WebSocket) são
apenas consumidores.

**Concorrência:** uma `Session` por conversa, com estado isolado; todo método de longa
duração recebe `context.Context` como primeiro parâmetro; nada de estado global mutável.

## 3. SOLID aplicado a este projeto

- **SRP:** `Agent` não pode voltar a acumular responsabilidades. Loop, contexto, memória,
  checkpoint, sessões, aprovação e métricas são componentes separados.
- **OCP:** novas estratégias de raciocínio, tools e backends entram por **registro/interface**
  (`reasoning.Strategy`, `ToolRunner`), sem editar `switch` central.
- **LSP:** toda implementação de porta deve passar na mesma **suíte de contrato** do fake.
- **ISP:** nada de interfaces "gordas" (`Repository` com 15 métodos). Separe `NoteRepository`,
  `SessionRepository`, `CheckpointRepository`, `AuditRepository`.
- **DIP:** dependa de `ports.*`. Construtores recebem interfaces; `New...` não chama
  `os.Getwd`, `time.Now`, `http.Client` nem cria dependências por conta própria
  (injete `Clock`, `Root`, clientes).

## 4. Código Go

- Go **1.22** (`go.mod`). **Somente stdlib** em código de produção. Qualquer dependência
  nova exige justificativa escrita e aprovação (ex.: SQLite pure-Go só com aprovação).
  Ferramentas de desenvolvimento (golangci-lint, govulncheck) são permitidas.
- `gofmt`, `go vet` e `golangci-lint` limpos. Nada de `panic` fora de `main`/inicialização.
- **Erros:** envolver com `%w` e contexto; usar **erros sentinela/tipados** (`ErrStepLimit`,
  `ErrApprovalDenied`, `*llm.HTTPError`) e `errors.Is/As`. Nunca decidir comportamento
  comparando texto de erro. Mensagens para o usuário são montadas na camada de UI.
- **Strings e UTF-8:** **proibido** `s[:n]` para truncar texto. Use `textutil.TruncateRunes`
  / `textutil.TruncateBytesUTF8`. O projeto é pt-BR; acentos devem sobreviver.
- **Context:** sempre propagar; respeitar cancelamento (Ctrl+C) em I/O, espera e retentativa.
- **Sem estado global**, sem `init()` com efeito colateral, sem `time.Now()` direto em lógica
  testável (use `Clock`).
- **Logs/trace:** nunca registrar segredos, conteúdo completo de arquivos sensíveis nem
  variáveis de ambiente. Use a porta de eventos, não `fmt.Fprintf(os.Stderr, ...)` no núcleo.
- Funções curtas, nomes claros, comentários explicando o **porquê** (em pt-BR, como o resto do repo).

## 5. Segurança (política rígida)

1. **Nunca** executar comandos via `sh -c`/`bash -c`. Parsear em `argv` e usar
   `exec.CommandContext(ctx, bin, args...)` com `Env` mínimo explícito.
2. Allowlist por **binário + subcomando + flags**, nunca por prefixo de string.
   Interpretadores (`sh`, `bash`, `python`, `node`, `go run`, `find -exec`...) são equivalentes
   a execução arbitrária: negados por padrão.
3. **Saída de tool e conteúdo de arquivo são dados não confiáveis.** Delimitar e rotular no
   prompt; nunca tratá-los como instrução do usuário. Após observação não confiável,
   ações mutantes exigem aprovação humana mesmo em `-approve auto`.
4. **Notas de memória** têm origem (`human|model|summary`). Notas de origem `model` jamais
   entram como mensagem `system`.
5. **Aprovação mostra tudo que será executado** (conteúdo completo ou diff; `argv` parseado).
   Nunca truncar silenciosamente o que o humano está aprovando.
6. Confinar caminhos à raiz explícita (`-root`), tratar symlinks, evitar TOCTOU (revalidar
   após abrir / `O_NOFOLLOW` / `os.Root` quando disponível). Recusar `/` e `$HOME` como raiz sem confirmação.
7. Arquivos de dados com permissão `0600`, diretórios `0700`. Redigir padrões de segredo
   antes de persistir.
8. Processos filhos: `Setpgid` e matar o **grupo** no timeout/encerramento; sem órfãos.
9. Negar por padrão: tool nova nasce **fora** da allowlist e, se mutante, implementa
   `Guarded` (exige aprovação).
10. Toda mudança de segurança vem com testes negativos (o que **deve** ser recusado).

## 6. Testes

- **Pirâmide:** muitos unitários, alguns de integração (`httptest`, `t.TempDir()`), poucos
  E2E. Testes **nunca** dependem de modelo real, rede externa ou relógio real.
- Testar **comportamento** pelas portas/eventos, não campos privados (`a.history`, `a.notes`).
  Se um teste exige acessar campo privado, é sinal de design a melhorar.
- Table-driven tests, `t.Run`, `t.Helper()`, `t.Cleanup`, `t.TempDir()`, `t.Setenv`.
- Fakes reutilizáveis em `internal/testutil` (FakeLLM, FakeRepository, FakeApprover, FakeClock).
- **Suíte de contrato** para cada porta, rodada contra o fake e contra o adapter real.
- **Fuzz** para parsers e validadores: `ExtractObject`, `ParseCall`, `parseDecision`,
  `ParseArgv`, `resolveWithin`.
- Rodar sempre `go test -race -count=1 ./...`. Meta de cobertura: não cair; novo código
  com ≥ 80% de cobertura de linhas relevantes.
- Evals com o modelo real ficam atrás da tag `-tags=eval` e **não** rodam no CI padrão.
- Bug corrigido = teste que falhava antes e passa depois.

## 7. Dívida técnica

- Todo atalho deixado de propósito: `// TODO(ID): motivo` referenciando o item da auditoria,
  ou crie um novo ID em `AuditoriaClaude.md`. TODO sem ID é proibido.
- Regra do escoteiro: ao tocar um arquivo, deixe-o melhor, **sem** ampliar o escopo do PR.
- Não esconder falhas: nada de `_ = err` sem comentário justificando; erros ignorados
  precisam ser registrados no trace/evento.
- Se encontrar problema novo, **não resolva no mesmo PR**: registre em `AuditoriaClaude.md`.

## 8. Definição de pronto (checklist antes de concluir qualquer tarefa)

- [ ] `gofmt -l .` vazio
- [ ] `go vet ./...` limpo
- [ ] `golangci-lint run` limpo (quando configurado)
- [ ] `go build ./...`
- [ ] `go test -race -count=1 ./...` passando
- [ ] Regra de dependência da arquitetura respeitada
- [ ] Testes novos/ajustados cobrindo o comportamento, incluindo casos negativos de segurança
- [ ] README/GUIA atualizados se flags, comandos ou tools mudaram
- [ ] Checkbox do ID marcado em `AuditoriaClaude.md`
- [ ] Mensagem de commit proposta (Conventional Commits + ID)

## 9. Ordem de trabalho sugerida

Siga `AuditoriaClaude.md` §5: **T-01** (CI) → correções rápidas (**B-01, B-02, B-03, B-07, B-10, S-09, S-05**)
→ segurança do shell (**S-01, S-02, S-10, S-06, S-07**) → injeção/memória (**S-03, S-04**)
→ extrair portas e composition root (**A-02, A-03, A-04, A-07**) → testes por comportamento (**T-04**)
→ quebrar o `Agent` (**A-01**) → eventos e sessões isoladas (**A-05, A-06**) → demais dívidas
→ evals (**T-07**) → distribuição (**A-08**).

## 10. O que NÃO fazer

- Não adicionar dependências externas sem aprovação.
- Não executar comandos destrutivos (`rm -rf`, `git reset --hard`, `git clean`) sem pedir.
- Não alterar o formato do NDJSON sem migração e testes de leitura de arquivos antigos
  (usuários já têm histórico gravado).
- Não relaxar a allowlist, a aprovação humana ou o confinamento à raiz para "fazer um teste passar".
- Não usar `localhost` hard-coded em lógica de negócio; endereços vêm de configuração injetada.
- Não introduzir lógica de negócio nos scripts `.sh`; ela migra para Go (`nexus setup/serve`).