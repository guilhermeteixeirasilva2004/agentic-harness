# Guia rápido — `agentic-harness`

Guia prático para instalar, subir o modelo e usar o agente no dia a dia.

**Repositório:** <https://github.com/guilhermeteixeirasilva2004/agentic-harness>

## Uso em 30 segundos

```bash
git clone https://github.com/guilhermeteixeirasilva2004/agentic-harness.git
cd agentic-harness
./install.sh     # compila o harness e instala o comando `nexus`
nexus            # abre o agente (sobe o servidor do modelo se preciso)
```

Precisa de Go 1.22+, do `llama.cpp` e de um modelo GGUF — veja a seção 1.
Os exemplos abaixo assumem que você está na raiz do repositório.

## 1. Pré-requisitos

- **Go 1.22+** para compilar o harness.
- **`llama.cpp`** compilado (`llama-server`).
- **Um modelo GGUF** baixado (ex.: `qwen2.5-3b-instruct-q4_k_m.gguf`).

O harness **não roda o modelo**: ele conversa via HTTP com um servidor
compatível com a API da OpenAI (`llama-server`).

## 2. Instalar (uma vez)

```bash
git clone https://github.com/guilhermeteixeirasilva2004/agentic-harness.git
cd agentic-harness
./install.sh          # compila e instala o comando `nexus` no PATH
```

Se você já tem o código em mãos, basta rodar o `./install.sh` na raiz do
repositório: ele compila a partir da própria pasta e não mexe no seu Go global.

Depois disso, **abra um terminal novo** e use só `nexus`.

Se preferir não instalar nada:

```bash
go build -o harness .
./harness help        # confere que o binário responde
```

Recompile (ou rode `./install.sh` de novo) sempre que alterar o código Go.
Depois de um `git pull`, rode `./install.sh` outra vez para reinstalar o `nexus`
já com as novidades.

## 3. Servidor do modelo (automático)

O `nexus` **sobe o servidor sozinho** se ele ainda não estiver no ar — na primeira
vez ele espera alguns segundos carregando o modelo. Para controlar na mão:

Opção pronta (build CUDA, todas as camadas na VRAM):

```bash
./scripts/serve.sh
```

Ou manualmente:

```bash
~/llama.cpp/build/bin/llama-server \
  -m ~/llama.cpp/models/qwen2.5-3b-instruct-q4_k_m.gguf \
  --host 127.0.0.1 --port 8080 -c 8192
```

Variantes do script:

```bash
NGL=20 ./scripts/serve.sh              # divide GPU (20 camadas) + CPU/RAM
BUILD=cpu NGL=0 ./scripts/serve.sh     # tudo na CPU
```

O harness usa `http://127.0.0.1:8080/v1` por padrão (mude com `-url` no binário ou
`HARNESS_URL` no `nexus`). Se o servidor estiver em outra máquina, use
`HARNESS_URL=http://host:8080/v1 NEXUS_AUTOSERVE=0 nexus` para o `nexus` não tentar
subir nada localmente.

## 4. Conversar

```bash
nexus                 # abre o agente já com persona e tools padrão
```

Pronto: escreva a mensagem e ele responde — usando ferramentas quando precisa.

O agente trabalha **no diretório onde você chamou o `nexus`** — então entre no
projeto antes (ou abra o terminal nele). Ele também recebe a **data e hora
atuais** no prompt, então acerta contas de idade, prazo e “que dia é hoje”.

Quem quiser controle fino pode usar o binário direto:

| Subcomando | O que faz |
|-----------|-----------|
| `chat` (padrão) | conversa livre com o modelo |
| `agent` | loop agêntico: o modelo **decide**, chama **tools**, observa e responde |

```bash
./harness chat          # conversa simples
./harness agent         # agente com as tools padrão (get_time, read_file)
./nexus.sh              # o mesmo que `nexus`, mas sem instalar
```

O `nexus` já vem com uma persona de agente de programação em pt-BR e libera as
tools mais usadas (`read_file`, `write_file`, `edit_file`, `run_shell`,
`run_shell_bg`, `wait`, `remember`, `ask_user`). Opções extras são repassadas:

```bash
nexus -session nexus -resume            # retoma a conversa anterior
nexus -temp 0 -verbose                  # determinístico, mostrando decisões
nexus -shellallow "go,ls,cat,git"       # libera mais comandos de shell
SYSTEM="Você é Nexus, fale como pirata." nexus   # troca a persona
```


## 5. Flags principais

| Flag | Para quê | Padrão |
|------|----------|--------|
| `-url` | endereço do servidor | `http://127.0.0.1:8080/v1` |
| `-model` | nome do modelo na requisição | `local` |
| `-system` | persona/system prompt | assistente em pt-BR |
| `-temp` | temperatura (`0` = determinístico) | `0.7` |
| `-maxtokens` | máximo de tokens por resposta | `0` (padrão do servidor) |
| `-stream` | streaming de tokens (chat e final do agente) | `true` |
| `-maxsteps` | passos do agente por rodada | `4` |
| `-allow` | tools liberadas, separadas por vírgula | `get_time,read_file` |
| `-strategy` | raciocínio: `react`, `cot` ou `plan` | `react` |
| `-maxcontext` | orçamento de contexto em tokens | `0` (sem limite) |
| `-verbose` | mostra decisões e chamadas de tool | `false` |
| `-approve` | aprovação de tools mutantes: `ask`, `auto`, `deny` | `ask` |
| `-shellallow` | prefixos liberados para `run_shell` | `ls,cat,grep,find,...` |
| `-retries` / `-backoff` | tentativas extras e espera inicial | `2` / `500ms` |
| `-rag` / `-topk` | RAG leve nas notas de memória | `false` / `5` |
| `-store` | arquivo NDJSON de sessões (`""` desativa) | `~/.local/share/...` |
| `-session` | identificador da sessão | gerado |
| `-resume` | retoma a última sessão persistida | `false` |
| `-continue` | retoma tarefa incompleta (checkpoint) | `false` |

Cada flag tem uma **variável de ambiente** equivalente (ex.: `HARNESS_ALLOW`,
`HARNESS_TEMP`, `HARNESS_MAXSTEPS`, `HARNESS_URL`, `HARNESS_STORE`).
Precedência: **flag > variável de ambiente > padrão**.

## 6. Tools disponíveis

| Tool | Argumentos | Precisa de aprovação? |
|------|-----------|----------------------|
| `get_time` | — | não |
| `read_file` | `path`, `max_bytes` | não |
| `list_dir` | `path` | não |
| `write_file` | `path`, `content`, `overwrite` | **sim** |
| `edit_file` | `path`, `old`, `new` | **sim** |
| `delete_file` | `path`, `recursive` | **sim** |
| `move_file` | `path`, `to`, `overwrite` | **sim** |
| `mkdir` | `path` | **sim** |
| `run_shell` | `command`, `cwd` | **sim** |
| `run_shell_bg` | `command`, `cwd` (devolve `job_id`) | **sim** |
| `wait` | `job_id`, `timeout` | não |
| `remember` | `note` | não |
| `ask_user` | `question` | não |

**Regra de ouro:** a allowlist **nega por padrão**. Uma tool só fica disponível
para o agente se estiver em `-allow`.

```bash
# libera leitura, escrita, shell e comandos em segundo plano
./harness agent -allow get_time,read_file,write_file,edit_file,run_shell,run_shell_bg,wait
```

As tools de leitura/inspeção continuam restritas à raiz do projeto (incluindo a
checagem de symlinks). `run_shell` soma três defesas: allowlist de **prefixos**
(`-shellallow`), recusa de metacaracteres de shell (`;`, `|`, `&`, `>`, `<`, ...)
e timeout de 10 s.

## 7. Aprovação humana

Com `-approve ask` (padrão), toda tool mutante pede confirmação no terminal:

```
Posso fazer isto: write_file {"path":"hello.txt","content":"oi"}
permitir? [s/N] s
```

| Modo | Comportamento |
|------|---------------|
| `ask` (padrão) | pergunta `s/N` a cada chamada |
| `auto` | aprova automaticamente (use com cuidado) |
| `deny` | nega sempre |

Dá para trocar ao vivo dentro do REPL: `/approve auto`.

## 8. Memória, sessões e retomada

```bash
./harness agent -session meuprojeto    # fixa a sessão (sempre a mesma)
./harness agent -resume                # retoma a última sessão persistida
./harness agent -store /tmp/s.jsonl    # arquivo de sessões próprio
./harness agent -store ""              # sem persistência
```

- **Notas de longo prazo**: `/remember <texto>` ou a tool `remember`.
  Veja com `/memory`; apague com `/forget`.
- **RAG leve**: `-rag -topk 5` busca notas por similaridade (usa `/embeddings`).
  Se o servidor não tiver embeddings, cai no comportamento anterior sem quebrar.
- **Retomar tarefa interrompida**: quando `-maxsteps` se esgota, o agente grava um
  *checkpoint* com o histórico e as observações já coletadas. Use
  `./harness agent -continue` (ou `/continue` no REPL) para continuar de onde
  parou — inclusive em outro processo.

## 9. Comandos do REPL

```
escrever uma mensagem     conversa com o agente
/ajuda   (/help)          mostra a ajuda
/limpar  (/clear)         começa uma conversa nova
/lembrar <txt>            guarda uma anotação para depois (/remember)
/continuar                retoma a tarefa anterior de onde parou (/continue)
/sair    (/exit, /quit)   encerra (ou Ctrl+C)

avançado:
/system <txt>             define o system prompt
/trace, /metrics          raciocínio do agente e números da sessão
/history                  histórico da conversa
/session [ls|show <id>]   lista sessões persistidas ou detalha uma
/memory                   memória de longo prazo (notas + resumo)
/esquecer (/forget)       esquece a memória da sessão atual
/approve [ask|auto|deny]  mostra ou altera o modo de aprovação
```

Dica: `ajuda`, `limpar`, `continuar`, `lembrar`, `esquecer` e `sair` também
funcionam **sem a barra** quando digitados sozinhos. O argumento pode vir com
`<`, `:` ou `(` no lugar do espaço — `lembrar<tenho 22 anos>`, `/lembrar:nota`
e `esquecer(tudo)` valem como `/lembrar tenho 22 anos`, etc. Se o nome estiver
errado (ex.: `/lembrarr`), o REPL sugere o mais parecido.

## 10. Receitas prontas

**Ler e resumir um arquivo do projeto**

```bash
./harness agent -temp 0 -verbose
# você> Leia internal/config/config.go e resuma em 3 linhas
```

**Criar um arquivo (com aprovação)**

```bash
./harness agent -allow write_file
# você> crie um arquivo notas.txt com "release v1"
# permitir? [s/N] s
```

**Rodar comandos longos (build/testes) sem travar o loop**

```bash
./harness agent -allow run_shell_bg,wait,run_shell -shellallow "go,ls"
# você> rode "go test ./..." e me diga se passou
```

O agente chama `run_shell_bg` (recebe um `job_id`) e depois `wait` para pegar a
saída. `wait` retorna em até 10 s por chamada — o agente pode repetir se o job
ainda estiver rodando.

**Tarefa com plano explícito**

```bash
./harness agent -strategy plan -maxsteps 6
```

**Sessão estável + memória**

```bash
./harness agent -session revisao -allow get_time,read_file,remember -rag
# você> lembre que este projeto não usa dependências externas
```

## 11. Dicas e solução de problemas

- **Determinismo**: use `-temp 0` em tarefas de código/leitura.
- **Depurar o raciocínio**: `-verbose` mostra cada decisão e chamada de tool;
  `/trace` mostra o trace completo e `/metrics` dá latência e tokens.
- **Economizar uma chamada**: `-stream=false` evita a chamada extra do streaming
  da resposta final (a resposta vem do JSON de decisão).
- **Tool não existe / foi negada**: confira se ela está em `-allow` — a allowlist
  nega por padrão.
- **`run_shell` bloqueado**: o comando precisa começar com um prefixo de
  `-shellallow` e não pode ter metacaractere de shell.
- **Sem resposta do modelo**: confirme que o `llama-server` está no ar em
  `127.0.0.1:8080` (ou ajuste `-url`); o harness já tenta de novo em falhas
  transitórias (`-retries`/`-backoff`).
- **Rodar os testes do projeto**: `go test ./...` (não precisa de modelo).

