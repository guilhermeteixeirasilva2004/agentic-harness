# agentic-harness

CLI em Go para conversar com um modelo local servido por uma API compatível com
OpenAI (por exemplo, o `llama-server` do llama.cpp).

A arquitetura desacopla o harness do motor de inferência:

```
[ CLI em Go ]  ⇄  HTTP (API compatível com OpenAI)  ⇄  [ llama-server ]  ⇄  [ modelo GGUF ]
```

📖 **Primeira vez aqui?** Veja o [guia rápido de uso](GUIA.md) — compilar, subir o
servidor, flags, tools, aprovação, memória e receitas prontas.

## Começando em 2 passos

```bash
./install.sh     # compila e instala o comando `nexus` no PATH
nexus            # conversa com o agente (o servidor sobe sozinho se preciso)
```

## Pré-requisitos

- Go (testado com 1.22)
- `llama-server` do llama.cpp compilado
- Um modelo GGUF baixado

Exemplo de como subir o servidor usado neste projeto:

```bash
~/llama.cpp/build/bin/llama-server \
  -m ~/llama.cpp/models/qwen2.5-3b-instruct-q4_k_m.gguf \
  --host 127.0.0.1 --port 8080 -c 8192
```

## Rodando na GPU (llama.cpp com CUDA)

O harness é **agnóstico ao motor**: fala HTTP com o `llama-server`. Para acelerar,
basta recompilar o llama.cpp com CUDA e subir o servidor com offload — **sem mudar
nada no código Go**.

Requisitos: GPU NVIDIA + toolkit CUDA (`nvcc`) instalados.

```bash
# 1) recompilar o llama.cpp com CUDA (gera ~/llama.cpp/build-cuda, não mexe no build CPU)
CUDA_ARCH=52 ./scripts/build-cuda.sh   # 52 = Maxwell (GTX 960/970/980)

# 2) servir com todas as camadas na VRAM (-ngl 99)
./scripts/serve.sh
```

Dividir entre GPU (VRAM) e CPU (RAM) com `-ngl` (quantas camadas vão para a GPU):

```bash
NGL=20 ./scripts/serve.sh          # 20 camadas na GPU, o resto na CPU/RAM
BUILD=cpu NGL=0 ./scripts/serve.sh # tudo na CPU (build original)
```

> Em placas antigas o `llama-server` avisa que **Flash Attention não é suportado**
> (ex.: Maxwell sm_52) e o desativa automaticamente — funciona, só um pouco mais lento.

## Decodificação especulativa

São **dois recursos independentes** (podem ser combinados):

- **Offload (`-ngl`)**: divide as camadas do modelo entre VRAM e RAM.
- **Especulativa (`-md` + `--spec-type draft-simple` + `--spec-draft-n-max N`)**:
  um modelo *draft* pequeno "rascunha" N tokens e o modelo alvo verifica em lote.
  ⚠️ `--spec-type` é **obrigatório** — o padrão é `none`, então `-md` sozinho **não liga**
  a decodificação especulativa nesta versão.

```bash
# baixar o draft Qwen2.5-0.5B (mesma família/tokenizer do alvo, ~491 MB)
wget -O ~/llama.cpp/models/qwen2.5-0.5b-instruct-q4_k_m.gguf \
  https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf

# draft 0,5B rascunhando 3 tokens por passo — config que mais rende no CPU
DRAFT=~/llama.cpp/models/qwen2.5-0.5b-instruct-q4_k_m.gguf SPEC_NMAX=3 SPEC_TD=8 ./scripts/serve.sh
```

### Regras que fazem a especulativa render (medido)

1. **Amostragem gulosa (`temperature = 0`) é obrigatória.** Com o padrão do
   llama.cpp (`temp=0.8`, `top_k=40`, `top_p=0.95`) a aceitação do draft despenca
   e a especulativa fica **mais lenta** que a geração normal. No 3B/CPU:
   `temp=0.8` → 12,7 t/s com spec vs 13,5 sem; `temp=0` → 15,6 vs 14,2.
2. **O draft precisa ser MUITO mais barato que o alvo.** Para o alvo 7B o draft
   **0,5B** ganha; o **1,5B** custa caro demais. Para o alvo 3B o 0,5B rende pouco
   (a verificação já é barata), mas ainda dá ~1,17×.
3. **`n-max` pequeno (3–4).** Valores altos (8–16) pagam por rascunhos rejeitados
   e derrubam o desempenho (7B: 5,3 t/s com n-max 8).
4. **`-td` (threads do draft) ajuda no CPU.** O 0,5B escala até ~8 threads;
   além disso (ex.: `-td 20`) há oversubscrição e tudo desaba.

### Resultados medidos — CPU (guloso, `-t 10`, `n=128`, Q4_K_M)

**7B** (baseline ~6,7 t/s):

| Configuração | Geração (t/s) | Aceleração | Aceitação | mean len |
|---|---|---|---|---|
| 7B sem especulativa | 6,7 | 1,00× | — | — |
| 7B + draft 1,5B, n-max 4 | 7,7 | 1,14× | 52,5% | 3,07 |
| 7B + draft 0,5B, n-max 4 | 8,2 | 1,21× | 39,3% | 2,57 |
| **7B + draft 0,5B, n-max 3, `-td 8`** | **8,3–9,1** | **1,22–1,35×** | 43,8% | 2,29 |
| 7B + draft 0,5B, n-max 8 | 5,3 | 0,79× | 26,0% | 3,02 |

**3B** (baseline ~14,8 t/s):

| Configuração | Geração (t/s) | Aceleração | Aceitação | mean len |
|---|---|---|---|---|
| 3B sem especulativa | 14,8 | 1,00× | — | — |
| 3B + draft 0,5B, n-max 4 | 15,6 | 1,10× | 48,5% | 2,89 |
| **3B + draft 0,5B, n-max 3, `-td 8`** | **17,4** | **1,17×** | 59,1% | 2,76 |
| 3B + draft 1,5B, n-max 4 | 11,3 | 0,76× | 60,5% | 3,34 |

> **GPU (GTX 960 4 GB / Maxwell sm_52):** aqui o ganho veio do **offload puro**
> (`-ngl 99`: 14,8 → 24,2 t/s, ~1,64×). Sem Flash Attention no Maxwell, a
> especulativa na GPU não compensou (verificação em lote sem aceleração).

> **Teto físico (CPU) e por que relatos de 1,8–2,5× não se reproduzem:** um
> 7B Q4_K_M (~4,4 GB) a ~34 GB/s de banda dá ~6–7 t/s sem draft — exatamente o
> baseline medido. No melhor ajuste a especulativa chega a ~9 t/s (+35%), e não a
> 11–14. Medindo a banda, o modo especulativo usa só ~24 GB/s dos ~34: sobra
> banda, ou seja, o gargalo virou o **overhead do loop de verificação em lote do
> llama.cpp no CPU**, não o hardware. Por isso uma implementação própria, com
> verificação em lote mais eficiente (ex.: Go com goroutines e GEMM batched
> cache-friendly), tem margem para passar do que o llama.cpp entrega hoje.

## Como testar o motor (benchmark)

Dois caminhos:

```bash
./scripts/bench.sh            # llama-cli: CPU x GPU x GPU+especulativa
go run ./cmd/specbench ...    # Go: matriz de configs, com aceitação do draft
```

O **`specbench`** (em Go) sobe um `llama-server` por caso, mede `/completion` em
modo guloso e lê a linha `draft acceptance` do log do servidor — é o mesmo
procedimento que gerou as tabelas acima:

```bash
go run ./cmd/specbench \
  -model ~/llama.cpp/models/qwen2.5-7b-instruct-q4_k_m.gguf \
  -draft ~/llama.cpp/models/qwen2.5-0.5b-instruct-q4_k_m.gguf \
  -t 10 -nmax 0,3,4,8 -td 0,8 -n 128
```

```
caso       ger (t/s)  acel.  aceit.  mean len  status
nmax0_td0  6.78       1.00x  -       -         ok
nmax3_td8  8.28       1.22x  43.8%   2.29      ok
```

Flags úteis: `-temp` (padrão 0 = guloso), `-gc` e `-memlimit` (controle de GC e
de memória do processo Go), `-json` (saída estruturada), `-ngl`/`-ngld`
(offload), `-spectype` (`draft-simple`, `ngram-simple`, ...).

### Resultados medidos — GPU (GTX 960 4 GB, Maxwell sm_52)

Qwen2.5-3B-Instruct Q4_K_M, prompt fixo, `-t 10`, build do llama.cpp `b12274`:

| Configuração | Prompt (t/s) | Geração (t/s) |
|---|---|---|
| CPU puro | 84,5 | 14,8 |
| **GPU `-ngl 99`** | ~90 | **24,2** |
| GPU + especulativa n-max 12 (draft na CPU) | 95,2 | 3,7 |
| GPU + especulativa n-max 12 (draft na GPU) | 95,1 | 4,1 |
| GPU + especulativa n-max 4 (draft na GPU) | 94,9 | 16,5 |

**Recomendação:** no CPU, **guloso + draft 0,5B + n-max 3 + `-td 8`** (1,2–1,35×);
nesta GPU, o ganho real vem do **offload puro** (`-ngl 99`, ~1,64×).

## Compilar e rodar

```bash
go build -o harness .
./harness            # chat (padrão)
./harness agent      # loop agêntico com tools
```

## Uso

O atalho mais simples é o comando `nexus` (ver [Começando em 2 passos](#começando-em-2-passos)):

```bash
nexus                # agente com persona + tools padrão
nexus -temp 0        # aceita qualquer flag do `harness agent`
```

Uso direto do binário:

```bash
./harness [chat|agent] [flags]
```

| Flag         | Descrição                                              | Padrão                     |
|--------------|--------------------------------------------------------|----------------------------|
| `-url`       | base URL do servidor compatível com OpenAI             | `http://127.0.0.1:8080/v1` |
| `-model`     | nome do modelo enviado na requisição                   | `local`                    |
| `-system`    | system prompt inicial                                  | persona em pt-BR           |
| `-temp`      | temperatura de amostragem                              | `0.7`                      |
| `-maxtokens` | máximo de tokens por resposta (0 = padrão do servidor) | `0`                        |
| `-stream`    | streaming de tokens no `chat` e na resposta final do `agent` | `true`              |
| `-maxsteps`  | máximo de passos por rodada do agente                  | `4`                        |
| `-strategy`  | estratégia de raciocínio: `react`, `cot` ou `plan`     | `react`                    |
| `-maxcontext`| orçamento de contexto em tokens (0 = sem limite)       | `0`                        |
| `-allow`     | tools permitidas, separadas por vírgula                | `get_time,read_file`       |
| `-embedmodel`| modelo usado em `/embeddings`                          | igual a `-model`           |
| `-rag`       | busca por similaridade nas notas de memória            | `false`                    |
| `-topk`      | quantas notas de memória injetar com `-rag` ligado     | `5`                        |
| `-retries`   | tentativas extras em falhas transitórias do modelo     | `2`                        |
| `-backoff`   | espera inicial entre retentativas (dobra a cada uma)   | `500ms`                    |
| `-verbose`   | mostra decisões e chamadas de tool do agente           | `false`                    |
| `-store`     | arquivo NDJSON de sessões (`""` desativa)              | `~/.local/share/...`       |
| `-session`   | identificador da sessão                                | gerado (`sess-<nano>`)     |
| `-resume`    | retoma o histórico da última sessão persistida         | `false`                    |
| `-continue`  | retoma a tarefa incompleta salva em checkpoint         | `false`                    |
| `-approve`   | aprovação de tools mutantes: `ask`, `auto` ou `deny`   | `ask`                      |
| `-shellallow`| prefixos de comando liberados para `run_shell`         | `ls,cat,grep,find,...`     |

Variáveis de ambiente equivalentes (usadas como padrão das flags):
`HARNESS_URL`, `HARNESS_MODEL`, `HARNESS_SYSTEM`, `HARNESS_TEMP`,
`HARNESS_MAXSTEPS`, `HARNESS_STRATEGY`, `HARNESS_MAXCONTEXT`, `HARNESS_ALLOW`,
`HARNESS_EMBED_MODEL`, `HARNESS_VERBOSE`, `HARNESS_STORE`, `HARNESS_SESSION`,
`HARNESS_RESUME`, `HARNESS_CONTINUE`, `HARNESS_APPROVE`, `HARNESS_SHELLALLOW`,
`HARNESS_RAG`, `HARNESS_TOPK`, `HARNESS_RETRIES`, `HARNESS_BACKOFF`.

## Agente com tools (Fase 0)

O tool calling é **orquestrado no Go**: o modelo só devolve um JSON válido, e o
harness valida, executa e realimenta a observação. Não dependemos do campo
`tool_calls` do servidor.

```
usuário → system prompt (persona + catálogo de tools) + histórico
        → 1ª chamada (json_schema): modelo decide
             {"action":"tool","tool":"read_file"} ou {"action":"final",...}
        → se for tool: 2ª chamada (json_schema = schema da própria tool)
             preenche os argumentos, que o Go valida contra o schema
        → executa a tool (com timeout) e injeta a observação no histórico
        → repete (até -maxsteps)
```

Contrato de decisão que o `json_schema` força o modelo a seguir:

```json
{"action": "tool",  "tool": "read_file"}
{"action": "final", "answer": "O módulo é agentic-harness."}
```

Quando a ação é `tool`, os argumentos saem de uma segunda chamada guiada pelo
JSON Schema da própria tool (`{"path": "go.mod", "max_bytes": 1000}`), então
também são garantidamente JSON válido — e ainda passam pela validação do Go.

Tools embutidas (read-only por padrão; as mutantes exigem aprovação):

| Tool         | Argumentos                     | Observação                                    |
|--------------|--------------------------------|-----------------------------------------------|
| `get_time`   | —                              | data/hora local                               |
| `read_file`  | `path`, `max_bytes` (opcional) | limitado à raiz do projeto e a 256 KiB        |
| `list_dir`   | `path` (opcional)              | lista o diretório, respeitando a raiz         |
| `write_file` | `path`, `content`, `overwrite` | cria/sobrescreve arquivo (até 1 MiB)           |
| `edit_file`  | `path`, `old`, `new`           | substitui um trecho exato e único              |
| `delete_file`| `path`, `recursive` (opcional) | remove arquivo; diretório exige `recursive`    |
| `move_file`  | `path`, `to`, `overwrite`      | move/renomeia (cria diretórios pais)           |
| `mkdir`      | `path`                         | cria diretório e pais, sempre sob a raiz       |
| `run_shell`  | `command`, `cwd` (opcional)    | comando simples com prefixo liberado, 10 s     |
| `run_shell_bg`| `command`, `cwd` (opcional)   | inicia em segundo plano e devolve `job_id`     |
| `wait`       | `job_id`, `timeout` (opcional) | aguarda o job e devolve a saída (somente leitura) |
| `remember`   | `note`                         | grava fato na memória de longo prazo           |
| `ask_user`   | `question`                     | pausa o loop e pergunta ao usuário no terminal |

Exemplo:

```bash
./harness agent -verbose -temp 0 -maxsteps 4 -allow get_time,read_file
```

```
você> Leia o go.mod e diga o nome do módulo.
[agente] resposta: {"action":"tool","tool":"read_file"}
[agente] decisão: tool=read_file args={"path": "go.mod", "max_bytes": 1000}
[agente] OBSERVAÇÃO (read_file): module agentic-harness
[agente] decisão: final
assistente> O nome do módulo declarado é 'agentic-harness'.
```

Guardrails atuais: allowlist **nega por padrão**, `read_file`/`list_dir` só
dentro da raiz do projeto (symlinks incluídos na checagem), timeout de 10 s por
tool, teto de passos (`-maxsteps`), limite de entrada/saída e histórico
reiniciável com `/clear`. Tools mutantes passam por **aprovação humana**. O
prompt do sistema inclui a **data e hora atuais**, o que evita erros em contas de
idade, prazo e “que dia é hoje”.

### Estratégias de raciocínio (Fase 2)

A flag `-strategy` escolhe como o modelo estrutura o raciocínio. Todas usam o
mesmo loop ReAct e o mesmo tool calling orquestrado no Go:

| Estratégia | O que muda                                                        |
|------------|-------------------------------------------------------------------|
| `react`    | padrão; decide entre `tool` e `final`, sem raciocínio explícito   |
| `cot`      | exige `reasoning` em cada decisão (cadeia de pensamento)          |
| `plan`     | começa com `{"action":"plan","plan":[...]}` e permite `replan`    |

O raciocínio fica num **trace estruturado** (`internal/cognition`): pensamento,
plano, chamada de tool, observação e resposta final, com estimativa de tokens e
compactação por orçamento. Ele **não** é reinjetado no histórico (economia de
contexto); serve para auditoria em `-verbose` e no comando `/trace`. Com
`-maxcontext`, as mensagens mais antigas são descartadas — preservando o system
prompt, o plano ativo e a última mensagem — para não estourar a janela.

```bash
./harness agent -strategy cot -maxcontext 4096 -verbose
./harness agent -strategy plan -maxsteps 6
```

Exemplo de trace (`/trace` ou `-verbose`):

```
 1. plan: ver a hora | responder
 2. tool_call: get_time {}
 3. observation (get_time): 2026-11-11 09:30:00
 4. final: São 09:30.
     pensamento: concluí o plano em dois passos
```

### Memória e observabilidade (Fase 3)

Cada turno do agente é persistido em um arquivo **NDJSON append-only** (uma
linha JSON por turno, sem dependências externas) e as métricas de execução são
acumuladas em memória. A persistência vem ligada por padrão em
`~/.local/share/agentic-harness/sessions.jsonl`; use `-store ""` para desativar
ou `HARNESS_STORE` para trocar o caminho.

O que é gravado por turno (`internal/store`):

```json
{"type":"turn","session":"sess-...","at":"2026-11-12T01:00:00Z","payload":{
  "input":"que horas são?","output":"São 01:00.","strategy":"react","model":"local",
  "duration":850000000,"steps":[{"n":1,"kind":"tool_call","tool":"get_time"}],
  "metrics":{"turns":1,"steps":2,"llm_calls":2,"tokens_in":95,"tokens_out":12,
             "llm_total":700000000,"tool_calls":1,"tool_total":1000000,
             "approvals":0,"denials":0,
             "wall_total":850000000,"per_tool":{"get_time":{"calls":1,"total":1000000}}}}}
```

Além dos turnos, o store guarda registros `memory` (notas de longo prazo),
`approval` (auditoria de aprovações) e `checkpoint` (estado da tarefa quando os
passos acabam sem resposta final, consumido por `-continue`).

Métricas (`internal/observ`): turnos, passos, chamadas LLM, tokens estimados de
entrada/saída, latência do LLM, latência e erros por tool e tempo de parede.
O arquivo é tolerante a linhas corrompidas (uma escrita interrompida não
invalida os turnos anteriores).

```bash
./harness agent -session revisao       # persiste em ~/.local/share/...
./harness agent -store /tmp/s.jsonl    # caminho próprio
./harness agent -store ""              # sem persistência
./harness agent -resume                # retoma a última sessão persistida
./harness agent -continue              # retoma a tarefa incompleta (checkpoint)
```

### Tools de OS e aprovação humana (Fase 4)

`write_file`, `edit_file`, `delete_file`, `move_file`, `mkdir` e `run_shell`
alteram o sistema. Elas continuam **bloqueadas pela allowlist** (`-allow ...`) e,
mesmo liberadas, só executam depois de uma aprovação humana:

| Modo (`-approve`) | Comportamento                                              |
|-------------------|------------------------------------------------------------|
| `ask` (padrão)    | pergunta `permitir? [s/N]` no terminal a cada chamada       |
| `auto`            | aprova automaticamente (use só em ambiente controlado)      |
| `deny`            | nega todas as tools mutantes                                |

O modo também muda em tempo de execução com `/approve ask|auto|deny`. Sem um
`Approver` conectado (execução não interativa, testes), tools mutantes são
**negadas por padrão**. Cada decisão vira um registro de auditoria no NDJSON:

```json
{"type":"approval","session":"sess-...","at":"...","payload":{"tool":"write_file","arguments":{"path":"a.txt"},"approved":true}}
```

`run_shell` soma três defesas: allowlist de **prefixos** (`-shellallow`), recusa
de metacaracteres (`;`, `|`, `>`, `$`, backtick, parênteses — sem pipes nem
redirecionamentos) e `cwd` confinado à raiz do projeto. Saída limitada a 64 KiB
e timeout de 10 s. `write_file` cria diretórios pais e limita o conteúdo a 1 MiB;
`edit_file` exige que o trecho `old` apareça **exatamente uma vez**.

O CRUD de arquivos fecha o ciclo de manutenção do projeto: `delete_file` recusa
remover a raiz do projeto e só apaga diretórios com `recursive=true`;
`move_file` recusa mover a raiz, mover um diretório para dentro de si mesmo e
sobrescrever o destino sem `overwrite=true`; `mkdir` cria pais sob demanda.
Todos validam origem/destino com `resolveWithin` (symlinks incluídos), de modo
que nenhuma operação sai da raiz do projeto.

```bash
./harness agent -allow get_time,read_file,write_file,edit_file,delete_file,move_file,mkdir,run_shell \
  -shellallow "ls,cat,grep,go test,git status" -verbose
```

```
você> crie o arquivo hello.txt com "oi"
Posso fazer isto: write_file {"path":"hello.txt","content":"oi"}
permitir? [s/N] s
[agente] OBSERVAÇÃO (write_file): arquivo gravado: hello.txt (2 bytes)
assistente> Arquivo hello.txt criado com o conteúdo "oi".
```

Quando o humano recusa:

```
Posso fazer isto: run_shell {"command":"rm -rf build"}
permitir? [s/N] n
[agente] ERRO (run_shell): APROVAÇÃO NEGADA (run_shell): a ferramenta não foi executada.
```

As métricas (`/metrics`) passam a contar aprovações e negações, além de
continuar registrando chamadas/erros por tool.

### Memória de longo prazo (Fase 5)

O agente mantém dois níveis de memória, em texto puro e sem dependências
externas:

1. **Resumo acumulado (memória de contexto)** — quando o histórico passa do
   orçamento `-maxcontext`, o `trimHistory` não descarta mensagens antigas às
   cegas: cada trecho removido vira uma linha do resumo
   (`- você: ...`, `- assistente: ...`, `- harness: OBSERVAÇÃO (read_file): ...`),
   reinjetado logo após o system prompt e preservado nos trims seguintes. O fio
   da conversa sobrevive a sessões longas sem chamadas extras ao modelo.
2. **Notas persistentes (memória entre sessões)** — fatos que precisam valer
   depois de fechar o programa são gravados no store NDJSON como registros
   `memory` e reinjetados no contexto ao abrir o harness (`LoadMemory`) ou ao
   retomar uma sessão (`-resume`).

```json
{"type":"memory","session":"sess-...","at":"...","payload":{"text":"projeto usa Go 1.22 e sem dependências externas"}}
```

As notas podem ser criadas pelo humano (`/remember`) ou pelo próprio modelo,
quando a tool `remember` está na allowlist:

```bash
./harness agent -allow get_time,read_file,remember
```

```
você> lembre que este projeto não usa dependências externas
[agente] decisão: tool=remember args={"note":"projeto não usa dependências externas"}
[agente] OBSERVAÇÃO (remember): nota gravada na memória de longo prazo
```

`/memory` mostra notas + resumo; `/forget` esquece a memória da sessão atual
(sem reescrever o NDJSON — as notas persistidas voltam no próximo
`LoadMemory`). Limites: 40 linhas e 8000 caracteres de resumo, 40 notas de até
500 caracteres.

### RAG leve na memória (Fase 6)

Com `-rag`, as notas deixam de ser injetadas em bloco: antes de cada turno o
harness pede `POST /v1/embeddings` (mesmo servidor, modelo `-embedmodel`) para a
mensagem do usuário e para cada nota, ranqueia por similaridade de cosseno e
injeta apenas as `-topk` mais relevantes.

```bash
./harness agent -rag -topk 3 -embedmodel nomic-embed-text -allow get_time,remember
```

- Vetores das notas são cacheados em memória; cada nota é embeddada uma vez.
- Falha silenciosa: `-rag` desligado, sem embedder ou erro no `/embeddings`
  voltam ao comportamento anterior (todas as notas), sem quebrar o turno.
- `/memory` marca com `[no contexto]` quais notas entraram no turno atual.

### Autonomia: retentativas, `ask_user` e `/continue` (Fase 6)

- **Retentativas com backoff exponencial** — toda chamada ao modelo passa por
  `Agent.complete`, que repete falhas transitórias (conexão recusada, timeout,
  5xx) até `-retries` vezes, esperando `-backoff`, `2×`, `4×`… Cancelamento do
  contexto (`Ctrl+C`) interrompe na hora. As tentativas aparecem em `/metrics`
  como `retentativas: N`.
- **`ask_user`** — o agente pode pausar e perguntar algo ao humano quando falta
  informação que só ele tem. No REPL a pergunta aparece como
  `[o agente pergunta] ...` e a resposta volta como observação. Sem canal
  interativo, a tool falha com erro explícito (sem aprovação: é leitura).
- **`/continue`** — quando `-maxsteps` se esgota, o erro sugere `/continue`; o
  histórico (com as observações já coletadas) é preservado, então o comando
  reenvia uma instrução de retomada e o agente conclui de onde parou. No REPL,
  `/continue` prefere o checkpoint persistido quando há um pendente.
- **Checkpoint + `-continue`** — ao esgotar os passos, o agente grava um registro
  `checkpoint` no store com o estado da tarefa (`input`, histórico completo,
  memória, notas, plano e estratégia). Assim a tarefa sobrevive ao fim do
  processo: `./harness agent -continue` (ou `HARNESS_CONTINUE=true`) restaura o
  checkpoint e roda o loop a partir dele, sem repetir o que já foi feito. Um
  turno bem-sucedido consome o checkpoint; um novo esgotamento grava outro.
- **Streaming real da resposta final** — com `-stream` (padrão), depois da
  decisão `final` o agente faz uma segunda chamada **em streaming** pedindo a
  resposta em texto corrido, e os pedaços aparecem ao vivo no terminal. Custa uma
  chamada extra ao modelo; se o streaming falhar ou vier vazio, cai na resposta
  já produzida no JSON de decisão.
- **`run_shell_bg` + `wait`** — para comandos longos (build, suíte de testes), o
  agente inicia o comando em segundo plano e recebe um `job_id`; `wait` aguarda
  (até 10 s por chamada — repita para continuar esperando) e devolve a saída
  acumulada. `run_shell_bg` exige aprovação e obedece à mesma
  allowlist/metacaracteres de `run_shell`; `wait` é somente leitura e não pede
  aprovação.

### Comandos no chat

Os mais usados têm atalhos em português entre parênteses. `ajuda`, `limpar`,
`continuar`, `lembrar`, `esquecer` e `sair` também funcionam **sem a barra**,
quando digitados sozinhos, e o argumento aceita `<`, `:` ou `(` no lugar do
espaço (`lembrar<tenho 22 anos>`).

- `/ajuda` (`/help`) — mostra a ajuda
- `/limpar` (`/clear`) — começa uma conversa nova
- `/lembrar <texto>` (`/remember`) — grava uma nota lembrada nas próximas conversas
- `/continuar` (`/continue`) — retoma a tarefa anterior de onde o agente parou
- `/sair` (`/exit`, `/quit`) — encerra (ou `Ctrl+C`)

Avançado:

- `/system <texto>` — define o system prompt
- `/trace` — mostra o raciocínio do agente (subcomando `agent`)
- `/metrics` — métricas acumuladas da sessão (subcomando `agent`)
- `/history` — histórico da conversa
- `/session [ls|show <id>]` — lista as sessões persistidas ou detalha uma
- `/memory` — mostra a memória de longo prazo (notas + resumo da conversa)
- `/esquecer` (`/forget`) — esquece a memória da sessão atual
- `/approve [ask|auto|deny]` — mostra ou altera o modo de aprovação (subcomando `agent`)

## Estrutura

```
GUIA.md                  guia rápido de uso (compilar, flags, tools, receitas)
install.sh               compila e instala o comando `nexus` no PATH
nexus.sh                 atalho para conversar com o agente (persona "Nexus")
main.go                  entrada, subcomandos e sinais
cmd/specbench            benchmark de especulativa (matriz + aceitação)
internal/config          configuração (flags + env)
internal/llm             cliente OpenAI-compatível: streaming, Complete e Embed
internal/cognition       trace estruturado do raciocínio (passos + orçamento)
internal/reasoning       estratégias react/cot/plan e schemas de decisão
internal/agent           loop ReAct com tool calling orquestrado em Go
                         (memória, RAG leve, retentativas, ask_user,
                          streaming do final e checkpoint para -continue)
internal/tools           contrato Tool, registry, allowlist e tools embutidas
internal/safety          guardrails (limites, allowlist, teto de passos)
internal/observ          métricas de execução (latência, tokens, tools)
internal/store           persistência NDJSON append-only de sessões
internal/specbench       ciclo de vida do llama-server + métricas
internal/ui              REPL de terminal (aprovação no terminal) + engine de chat
scripts/                 build CUDA, subir servidor e benchmark
```

## Testes

```bash
go test ./...          # unitários + integração (httptest, sem modelo real)
gofmt -l . && go vet ./... && go build ./...
```

`internal/agent` tem um E2E em Go: um `httptest` que imita o servidor
compatível com OpenAI (`/v1/chat/completions`) e roda o loop completo —
decisão → tool → observação → `final`, incluindo `remember`, `ask_user`,
retentativa sobre HTTP (`503` e depois `200`) e a resposta final em streaming
(SSE). Há também testes de checkpoint/`-continue`, em que um segundo agente
restaura o estado salvo pelo primeiro. Nenhum processo externo (Python ou
modelo) é necessário.
