// Package ui implementa o loop de conversa (REPL) no terminal.
package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"agentic-harness/internal/agent"
	"agentic-harness/internal/config"
)

// Engine é o motor de uma rodada de conversa: chat simples ou agente com tools.
type Engine interface {
	// Reply processa a entrada do usuário e devolve a resposta final,
	// opcionalmente emitindo pedaços via onDelta.
	Reply(ctx context.Context, input string, onDelta func(string)) (string, error)
	// SetSystem define (ou limpa, se vazio) o system prompt.
	SetSystem(system string)
	// Clear reinicia o histórico da conversa.
	Clear()
}

// Resumer é o caso de uso de retomada (/continue) oferecido pelo motor.
// O motor decide internamente entre restaurar um checkpoint salvo e aproveitar
// o histórico já em memória; a UI não precisa saber qual dos dois ocorreu.
type Resumer interface {
	ResumeTask(ctx context.Context, onDelta func(string)) (string, error)
}

// REPL mantém a interface de terminal e delega as rodadas ao Engine.
type REPL struct {
	cfg       *config.Config
	eng       Engine
	in        *bufio.Scanner
	approve   string
	agentMode bool
}

// New cria um REPL a partir da configuração e do motor escolhido. Quando o motor
// aceita um Approver (agente), o próprio REPL passa a aprovar tools mutantes.
func New(cfg *config.Config, eng Engine) *REPL {
	r := &REPL{
		cfg:     cfg,
		eng:     eng,
		in:      bufio.NewScanner(os.Stdin),
		approve: config.NormalizeApprove(cfg.Approve),
	}
	if setter, ok := eng.(interface{ SetApprover(agent.Approver) }); ok {
		setter.SetApprover(r)
		r.agentMode = true
	}
	if setter, ok := eng.(interface{ SetAsker(agent.Asker) }); ok {
		setter.SetAsker(r)
	}
	return r
}

// Ask responde às perguntas da tool ask_user usando o mesmo terminal do REPL.
func (r *REPL) Ask(_ context.Context, question string) (string, error) {
	question = strings.TrimSpace(question)
	if question != "" {
		fmt.Printf("\n[o agente pergunta] %s\n", question)
	}
	fmt.Print("você> ")
	if !r.in.Scan() {
		return "", errors.New("entrada encerrada")
	}
	return r.in.Text(), nil
}

// Run inicia o loop de conversa até EOF, /exit ou Ctrl+C.
func (r *REPL) Run(ctx context.Context) {
	fmt.Println("⚡ Pronto para conversar — escreva sua mensagem.")
	fmt.Println("   /ajuda mostra as opções · /sair encerra")
	if r.cfg.Verbose {
		fmt.Printf("   [debug] servidor: %s | modelo: %s\n", r.cfg.BaseURL, r.cfg.Model)
		if r.agentMode {
			fmt.Printf("   [debug] aprovação: %s (tools mutantes: write_file, edit_file, delete_file, move_file, mkdir, run_shell)\n", r.approve)
		}
	}
	fmt.Println()

	for {
		if ctx.Err() != nil {
			fmt.Println()
			return
		}
		fmt.Print("você> ")
		if !r.in.Scan() {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(r.in.Text())
		if line == "" {
			continue
		}
		line = normalizeCommand(line)
		if strings.HasPrefix(line, "/") {
			if r.handleCommand(ctx, line) {
				return
			}
			continue
		}

		fmt.Print("assistente> ")
		reply, err := r.eng.Reply(ctx, line, func(delta string) {
			fmt.Print(delta)
		})
		if err != nil {
			fmt.Println()
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "[erro] %v\n", err)
			continue
		}
		if !strings.HasSuffix(reply, "\n") {
			fmt.Println()
		}
	}
}

// handleCommand trata comandos que começam com "/". Retorna true para sair.
func (r *REPL) handleCommand(ctx context.Context, line string) bool {
	line = normalizeCommand(line)
	rawName, arg := splitCommand(line)
	name := rawName
	if canonical, ok := commandAliases[name]; ok {
		name = canonical
	}
	switch name {
	case "/exit", "/quit":
		return true
	case "/clear":
		r.eng.Clear()
		fmt.Println("[histórico limpo]")
	case "/system":
		arg = strings.TrimSpace(arg)
		r.eng.SetSystem(arg)
		if arg == "" {
			fmt.Println("[system prompt removido]")
		} else {
			fmt.Println("[system prompt atualizado]")
		}
	case "/trace":
		tracer, ok := r.eng.(interface{ Trace() string })
		if !ok {
			fmt.Println("[trace indisponível no modo chat]")
			break
		}
		out := strings.TrimSpace(tracer.Trace())
		if out == "" {
			out = "(trace vazio)"
		}
		fmt.Println(out)
	case "/metrics":
		m, ok := r.eng.(interface{ Metrics() string })
		if !ok {
			fmt.Println("[métricas indisponíveis no modo chat]")
			break
		}
		fmt.Println(strings.TrimSpace(m.Metrics()))
	case "/history":
		h, ok := r.eng.(interface{ History() string })
		if !ok {
			fmt.Println("[histórico indisponível neste modo]")
			break
		}
		fmt.Println(strings.TrimSpace(h.History()))
	case "/session":
		ls, ok := r.eng.(interface{ Sessions() string })
		if !ok {
			fmt.Println("[sessões indisponíveis no modo chat]")
			break
		}
		fields := strings.Fields(arg)
		if len(fields) == 0 || fields[0] == "ls" || fields[0] == "list" {
			fmt.Println(strings.TrimSpace(ls.Sessions()))
			break
		}
		id := fields[0]
		if fields[0] == "show" {
			id = ""
			if len(fields) > 1 {
				id = fields[1]
			}
		}
		if show, ok := r.eng.(interface{ ShowSession(string) string }); ok {
			fmt.Println(strings.TrimSpace(show.ShowSession(id)))
		}
	case "/memory":
		m, ok := r.eng.(interface{ Memory() string })
		if !ok {
			fmt.Println("[memória indisponível no modo chat]")
			break
		}
		fmt.Println(strings.TrimSpace(m.Memory()))
	case "/remember":
		rem, ok := r.eng.(interface{ Remember(string) error })
		if !ok {
			fmt.Println("[memória indisponível no modo chat]")
			break
		}
		arg = strings.TrimSpace(arg)
		if arg == "" {
			fmt.Printf("uso: %s <texto a lembrar nas próximas conversas>\n", rawName)
			break
		}
		if err := rem.Remember(arg); err != nil {
			fmt.Printf("[erro] %v\n", err)
			break
		}
		fmt.Println("[nota gravada na memória de longo prazo]")
	case "/forget":
		f, ok := r.eng.(interface{ Forget() })
		if !ok {
			fmt.Println("[memória indisponível no modo chat]")
			break
		}
		f.Forget()
		fmt.Println("[memória da sessão esquecida]")
	case "/continue":
		if !r.agentMode {
			fmt.Println("[continue indisponível no modo chat]")
			break
		}
		fmt.Print("assistente> ")
		onDelta := func(delta string) { fmt.Print(delta) }
		// O caso de uso de retomada vive no núcleo (agent.ResumeTask): ele decide
		// entre restaurar o checkpoint salvo e reaproveitar o histórico atual.
		res, ok := r.eng.(Resumer)
		if !ok {
			fmt.Println("[continue indisponível neste motor]")
			break
		}
		reply, err := res.ResumeTask(ctx, onDelta)
		if err != nil {
			fmt.Println()
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return true
			}
			fmt.Fprintf(os.Stderr, "[erro] %v\n", err)
			break
		}
		if !strings.HasSuffix(reply, "\n") {
			fmt.Println()
		}
	case "/approve":
		if !r.agentMode {
			fmt.Println("[aprovação indisponível no modo chat]")
			break
		}
		arg = strings.TrimSpace(arg)
		if arg == "" {
			fmt.Printf("modo de aprovação: %s (ask|auto|deny)\n", r.approve)
			break
		}
		fmt.Printf("[modo de aprovação: %s]\n", r.setApproveMode(arg))
	case "/help":
		fmt.Println()
		fmt.Println("o que você pode fazer:")
		fmt.Println("  escrever uma mensagem    conversa com o agente")
		fmt.Println("  /ajuda                   mostra esta ajuda")
		fmt.Println("  /limpar                  começa uma conversa nova")
		fmt.Println("  /lembrar <texto>         guarda uma anotação para depois")
		fmt.Println("  /continuar               retoma a tarefa anterior de onde parou")
		fmt.Println("  /sair                    encerra o programa")
		fmt.Println()
		fmt.Println("avançado:")
		fmt.Println("  /system <texto>          define a personalidade (system prompt)")
		fmt.Println("  /history                 mostra a conversa até agora")
		fmt.Println("  /memory                  mostra as anotações guardadas")
		fmt.Println("  /esquecer                apaga as anotações desta sessão")
		fmt.Println("  /session [ls|show <id>]  lista ou detalha conversas salvas")
		fmt.Println("  /approve [ask|auto|deny] mostra ou muda a confirmação de ações")
		fmt.Println("  /metrics, /trace         números da sessão e raciocínio do agente")
		fmt.Println()
		fmt.Println("dica: ajuda, limpar, continuar, lembrar, esquecer e sair também funcionam sem a barra.")
	default:
		if sug := suggestCommand(rawName); sug != "" {
			fmt.Printf("comando desconhecido: %s — você quis dizer %s? (use /help)\n", rawName, sug)
		} else {
			fmt.Printf("comando desconhecido: %s (use /help)\n", rawName)
		}
	}
	return false
}

// commandAliases aceita atalhos em português para os comandos mais usados.
var commandAliases = map[string]string{
	"/ajuda":     "/help",
	"/sair":      "/exit",
	"/limpar":    "/clear",
	"/lembrar":   "/remember",
	"/esquecer":  "/forget",
	"/continuar": "/continue",
}

// barewordCommands permite digitar o comando sem a barra inicial, desde que a
// palavra esteja sozinha na linha ("sair", "ajuda", "limpar"). Frases que apenas
// começam com essas palavras ("limpar o cache") continuam indo para o agente.
var barewordCommands = map[string]string{
	"ajuda":     "/ajuda",
	"sair":      "/sair",
	"limpar":    "/limpar",
	"continuar": "/continuar",
	"lembrar":   "/lembrar",
	"esquecer":  "/esquecer",
}

// normalizeCommand aceita as variações comuns de digitação do comando:
// "sair" e "ajuda" (sem a barra, sozinhos) e "lembrar<texto>",
// "lembrar:nota", "/esquecer(algo)" (delimitador em vez de espaço).
func normalizeCommand(line string) string {
	text := strings.TrimSpace(line)
	body := strings.TrimPrefix(text, "/")
	lower := strings.ToLower(body)
	for _, name := range commandNames() {
		if len(lower) <= len(name) || !strings.HasPrefix(lower, name) {
			continue
		}
		rest := body[len(name)+1:]
		switch body[len(name)] {
		case '<', '(':
			// delimitadores inequívocos: valem com ou sem espaço.
		case ':':
			// só quando colado no argumento ("lembrar:nota"); "ajuda: faça X" é
			// uma frase normal e deve ir para o agente.
			if strings.HasPrefix(rest, " ") {
				continue
			}
		default:
			continue
		}
		arg := strings.TrimRight(strings.TrimSpace(rest), ">)")
		if arg == "" {
			return "/" + name
		}
		return "/" + name + " " + arg
	}
	if !strings.HasPrefix(text, "/") {
		if cmd, ok := barewordCommands[lower]; ok {
			return cmd
		}
	}
	return text
}

// commandNames lista os comandos reconhecidos sem a barra inicial.
func commandNames() []string {
	known := knownCommands()
	names := make([]string, 0, len(known))
	for _, cmd := range known {
		names = append(names, strings.TrimPrefix(cmd, "/"))
	}
	return names
}

// knownCommands lista todos os comandos reconhecidos pelo REPL, com a barra.
func knownCommands() []string {
	cmds := []string{
		"/exit", "/quit", "/clear", "/system", "/trace", "/metrics",
		"/history", "/session", "/memory", "/remember", "/forget",
		"/continue", "/approve", "/help",
	}
	for alias := range commandAliases {
		cmds = append(cmds, alias)
	}
	return cmds
}

// suggestCommand devolve o comando conhecido mais próximo do que foi digitado
// (pelo prefixo mais longo), ou "" quando não há sugestão razoável.
func suggestCommand(name string) string {
	if !strings.HasPrefix(name, "/") || name == "/" {
		return ""
	}
	best := ""
	for _, cmd := range knownCommands() {
		if len(cmd) < len(name) && strings.HasPrefix(name, cmd) && len(cmd) > len(best) {
			best = cmd
		}
	}
	return best
}

func splitCommand(line string) (string, string) {
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return line[:i], line[i+1:]
	}
	return line, ""
}
