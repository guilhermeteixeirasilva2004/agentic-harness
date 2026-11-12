// Command agentic-harness é um CLI simples para conversar com um modelo local
// servido por uma API compatível com OpenAI (ex.: llama-server).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"agentic-harness/internal/agent"
	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/store"
	"agentic-harness/internal/ui"
)

func main() {
	sub, args := splitSubcommand(os.Args[1:])

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch sub {
	case "help":
		usage()
		return
	case "", "chat":
		cfg := config.Load(args)
		ui.New(cfg, ui.NewChat(cfg, llm.NewClient(cfg.BaseURL))).Run(ctx)
	case "agent":
		cfg := config.Load(args)
		a := agent.NewDefault(cfg)
		a.SetSession(cfg.Session)
		if cfg.StorePath != "" {
			st, err := store.Open(cfg.StorePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[aviso] persistência desativada: %v\n", err)
			} else {
				defer st.Close()
				a.SetStore(st)
				if err := a.LoadMemory(); err != nil {
					fmt.Fprintf(os.Stderr, "[aviso] memória de longo prazo não carregada: %v\n", err)
				}
				if cfg.Resume {
					if err := a.Resume(); err != nil {
						fmt.Fprintf(os.Stderr, "[aviso] não foi possível retomar: %v\n", err)
					} else {
						fmt.Printf("[sessão %s retomada]\n", a.SessionID())
					}
				}
			}
		}
		// O REPL define o canal de aprovação/perguntas; cria-lo antes permite que
		// a retomada por -continue use ask_user quando precisar.
		repl := ui.New(cfg, a)
		if cfg.Continue {
			if out, err := a.Continue(ctx, nil); err != nil {
				fmt.Fprintf(os.Stderr, "[aviso] não foi possível continuar: %v\n", err)
			} else {
				fmt.Println("assistente>", strings.TrimSpace(out))
			}
		}
		repl.Run(ctx)
	default:
		fmt.Fprintf(os.Stderr, "subcomando desconhecido: %q\n\n", sub)
		usage()
		os.Exit(2)
	}
}

// splitSubcommand separa o subcomando dos demais argumentos. Um primeiro
// argumento que começa com "-" é tratado como flag, logo o subcomando é "chat".
func splitSubcommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	if strings.HasPrefix(args[0], "-") {
		return "", args
	}
	return args[0], args[1:]
}

func usage() {
	fmt.Println("uso: agentic-harness [chat|agent] [flags]")
	fmt.Println()
	fmt.Println("subcomandos:")
	fmt.Println("  chat    conversa livre com o modelo (padrão, com streaming)")
	fmt.Println("  agent   loop agêntico: o modelo decide, chama tools e observa o resultado")
	fmt.Println()
	fmt.Println("flags principais:")
	fmt.Println("  -url, -model, -system, -temp, -maxtokens, -stream")
	fmt.Println("  -maxsteps, -allow, -embedmodel   (controle do agente)")
	fmt.Println("  -rag, -topk                      (RAG leve nas notas de memória)")
	fmt.Println("  -retries, -backoff               (retentativas com backoff exponencial)")
	fmt.Println("  -strategy, -maxcontext           (raciocínio: react|cot|plan e orçamento de contexto)")
	fmt.Println("  -store, -session, -resume        (persistência de sessões)")
	fmt.Println("  -continue                        (retoma tarefa incompleta salva em checkpoint)")
	fmt.Println("  -approve, -shellallow            (aprovação de tools mutantes: ask|auto|deny)")
	fmt.Println()
	fmt.Println("variáveis: HARNESS_URL, HARNESS_MODEL, HARNESS_SYSTEM, HARNESS_TEMP,")
	fmt.Println("           HARNESS_MAXSTEPS, HARNESS_ALLOW, HARNESS_EMBED_MODEL,")
	fmt.Println("           HARNESS_RAG, HARNESS_TOPK, HARNESS_RETRIES, HARNESS_BACKOFF,")
	fmt.Println("           HARNESS_STRATEGY, HARNESS_MAXCONTEXT,")
	fmt.Println("           HARNESS_STORE, HARNESS_SESSION, HARNESS_RESUME, HARNESS_CONTINUE,")
	fmt.Println("           HARNESS_APPROVE, HARNESS_SHELLALLOW")
}
