package ui

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"agentic-harness/internal/agent"
	"agentic-harness/internal/config"
)

// agentEngine imita o agente: aceita Approver/Asker (logo, agentMode = true) e
// expõe o caso de uso de retomada (ResumeTask).
type agentEngine struct {
	inputs  []string
	resumes int
	asker   agent.Asker
}

func (f *agentEngine) Reply(_ context.Context, input string, onDelta func(string)) (string, error) {
	f.inputs = append(f.inputs, input)
	if onDelta != nil {
		onDelta("ok")
	}
	return "ok", nil
}

func (f *agentEngine) ResumeTask(_ context.Context, onDelta func(string)) (string, error) {
	f.resumes++
	if onDelta != nil {
		onDelta("ok")
	}
	return "ok", nil
}

func (f *agentEngine) SetSystem(string)           {}
func (f *agentEngine) Clear()                     {}
func (f *agentEngine) SetApprover(agent.Approver) {}
func (f *agentEngine) SetAsker(as agent.Asker)    { f.asker = as }

// chatEngine imita o modo chat: não aceita Approver nem Asker.
type chatEngine struct{ inputs []string }

func (f *chatEngine) Reply(_ context.Context, input string, _ func(string)) (string, error) {
	f.inputs = append(f.inputs, input)
	return "ok", nil
}

func (f *chatEngine) SetSystem(string) {}
func (f *chatEngine) Clear()           {}

// memoryEngine imita o agente com memória de longo prazo disponível.
type memoryEngine struct {
	chatEngine
	notes []string
}

func (f *memoryEngine) Remember(text string) error {
	f.notes = append(f.notes, text)
	return nil
}

// captureStdout troca os.Stdout por um pipe durante fn e devolve o que saiu.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

func TestREPLAskLeDoTerminal(t *testing.T) {
	eng := &agentEngine{}
	r := New(&config.Config{Approve: "ask"}, eng)
	r.in = bufio.NewScanner(strings.NewReader("Maria\n"))

	if eng.asker == nil {
		t.Fatal("New deveria registrar o REPL como asker do agente")
	}

	var answer string
	var err error
	out := captureStdout(t, func() {
		answer, err = r.Ask(context.Background(), "qual o nome?")
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if answer != "Maria" {
		t.Fatalf("resposta = %q", answer)
	}
	if !strings.Contains(out, "qual o nome?") {
		t.Fatalf("a pergunta não foi exibida: %q", out)
	}
}

func TestREPLAskSemEntrada(t *testing.T) {
	r := New(&config.Config{}, &agentEngine{})
	r.in = bufio.NewScanner(strings.NewReader(""))

	var err error
	captureStdout(t, func() { _, err = r.Ask(context.Background(), "algo?") })
	if err == nil {
		t.Fatal("esperava erro quando a entrada termina")
	}
}

func TestREPLContinueRetomaTarefa(t *testing.T) {
	eng := &agentEngine{}
	r := New(&config.Config{Approve: "ask"}, eng)
	if !r.agentMode {
		t.Fatal("engine com SetApprover deveria ativar o agentMode")
	}

	exit := r.handleCommand(context.Background(), "/continue")
	if exit {
		t.Fatal("/continue não deveria encerrar o REPL")
	}
	if eng.resumes != 1 || len(eng.inputs) != 0 {
		t.Fatalf("resumes = %d, entradas = %#v", eng.resumes, eng.inputs)
	}
}

func TestREPLContinueSoNoModoAgente(t *testing.T) {
	eng := &chatEngine{}
	r := New(&config.Config{}, eng)

	captureStdout(t, func() { r.handleCommand(context.Background(), "/continue") })
	if len(eng.inputs) != 0 {
		t.Fatalf("no modo chat /continue não deveria chamar o modelo: %#v", eng.inputs)
	}
}

func TestREPLExit(t *testing.T) {
	r := New(&config.Config{}, &chatEngine{})
	if !r.handleCommand(context.Background(), "/exit") {
		t.Fatal("/exit deveria encerrar o REPL")
	}
}

func TestREPLApelidosEmPortugues(t *testing.T) {
	// /sair encerra o REPL.
	r := New(&config.Config{}, &chatEngine{})
	if !r.handleCommand(context.Background(), "/sair") {
		t.Fatal("/sair deveria encerrar o REPL")
	}

	// /continuar age como /continue.
	eng := &agentEngine{}
	r2 := New(&config.Config{Approve: "ask"}, eng)
	if r2.handleCommand(context.Background(), "/continuar") {
		t.Fatal("/continuar não deveria encerrar o REPL")
	}
	if eng.resumes != 1 || len(eng.inputs) != 0 {
		t.Fatalf("resumes = %d, entradas = %#v", eng.resumes, eng.inputs)
	}

	// /limpar e /ajuda respondem em português.
	r3 := New(&config.Config{}, &chatEngine{})
	if out := captureStdout(t, func() { r3.handleCommand(context.Background(), "/limpar") }); !strings.Contains(out, "histórico limpo") {
		t.Fatalf("/limpar: saída = %q", out)
	}
	out := captureStdout(t, func() { r3.handleCommand(context.Background(), "/ajuda") })
	if !strings.Contains(out, "/sair") || !strings.Contains(out, "/continuar") {
		t.Fatalf("/ajuda deveria listar os atalhos: %q", out)
	}

	// /lembrar e /esquecer caem nas branches de memória (indisponíveis no chat).
	if out := captureStdout(t, func() { r3.handleCommand(context.Background(), "/lembrar anote isso") }); !strings.Contains(out, "memória indisponível") {
		t.Fatalf("/lembrar deveria cair em /remember: %q", out)
	}
	if out := captureStdout(t, func() { r3.handleCommand(context.Background(), "/esquecer") }); !strings.Contains(out, "memória indisponível") {
		t.Fatalf("/esquecer deveria cair em /forget: %q", out)
	}
}

func TestREPLComandoSemBarra(t *testing.T) {
	// normalizeCommand aceita a palavra sozinha e o delimitador no lugar do espaço.
	cases := map[string]string{
		"ajuda":                   "/ajuda",
		"SAIR":                    "/sair",
		"limpar":                  "/limpar",
		"continuar":               "/continuar",
		"lembrar":                 "/lembrar",
		"esquecer":                "/esquecer",
		"lembrar<guilherme>":      "/lembrar guilherme",
		"/lembrar<tenho 22 anos>": "/lembrar tenho 22 anos",
		"lembrar:anote isso":      "/lembrar anote isso",
		"ajuda: faça X":           "ajuda: faça X",
		"limpar o cache":          "limpar o cache",
		"o que é limpar":          "o que é limpar",
	}
	for in, want := range cases {
		if got := normalizeCommand(in); got != want {
			t.Fatalf("normalizeCommand(%q) = %q, esperava %q", in, got, want)
		}
	}

	// "sair" sem barra encerra o REPL e "continuar" age como /continue.
	eng := &agentEngine{}
	r := New(&config.Config{Approve: "ask"}, eng)
	if out := captureStdout(t, func() { r.handleCommand(context.Background(), normalizeCommand("continuar")) }); !strings.Contains(out, "assistente>") {
		t.Fatalf("\"continuar\" deveria disparar /continue: %q", out)
	}
	if eng.resumes != 1 || len(eng.inputs) != 0 {
		t.Fatalf("resumes = %d, entradas = %#v", eng.resumes, eng.inputs)
	}

	// Frase comum vai para o modelo, não vira comando.
	chat := &chatEngine{}
	r2 := New(&config.Config{}, chat)
	r2.in = bufio.NewScanner(strings.NewReader("limpar o cache\nsair\n"))
	captureStdout(t, func() { r2.Run(context.Background()) })
	if len(chat.inputs) != 1 || chat.inputs[0] != "limpar o cache" {
		t.Fatalf("a frase deveria ir para o modelo: %#v", chat.inputs)
	}
}

func TestREPLLembrarComDelimitador(t *testing.T) {
	eng := &memoryEngine{}
	r := New(&config.Config{}, eng)
	captureStdout(t, func() { r.handleCommand(context.Background(), "/lembrar<tenho 22 anos>") })
	if len(eng.notes) != 1 || eng.notes[0] != "tenho 22 anos" {
		t.Fatalf("notas = %#v", eng.notes)
	}
}

func TestREPLUsoLembrarEmPortugues(t *testing.T) {
	r := New(&config.Config{}, &memoryEngine{})
	out := captureStdout(t, func() { r.handleCommand(context.Background(), "/lembrar") })
	if !strings.Contains(out, "uso: /lembrar") {
		t.Fatalf("o uso deveria refletir o comando digitado em pt-BR: %q", out)
	}
	if strings.Contains(out, "/remember") {
		t.Fatalf("o uso não deveria citar o comando em inglês: %q", out)
	}
}

func TestREPLSugereComandoParecido(t *testing.T) {
	r := New(&config.Config{}, &chatEngine{})
	out := captureStdout(t, func() { r.handleCommand(context.Background(), "/lembrarr guardar") })
	if !strings.Contains(out, "você quis dizer /lembrar?") {
		t.Fatalf("esperava sugestão de /lembrar: %q", out)
	}

	out = captureStdout(t, func() { r.handleCommand(context.Background(), "/xyz") })
	if !strings.Contains(out, "comando desconhecido: /xyz") || strings.Contains(out, "quis dizer") {
		t.Fatalf("sem comando parecido, não deveria sugerir: %q", out)
	}
}

func TestREPLBannerEscondeDetalhesSemVerbose(t *testing.T) {
	r := New(&config.Config{BaseURL: "http://exemplo:8080/v1", Model: "m"}, &agentEngine{})
	r.in = bufio.NewScanner(strings.NewReader("/sair\n"))
	out := captureStdout(t, func() { r.Run(context.Background()) })
	if strings.Contains(out, "http://exemplo:8080/v1") {
		t.Fatalf("banner não deveria mostrar o servidor sem -verbose: %q", out)
	}
	if !strings.Contains(out, "/ajuda") || !strings.Contains(out, "/sair") {
		t.Fatalf("banner deveria citar /ajuda e /sair: %q", out)
	}
}

func TestREPLBannerVerboseMostraServidor(t *testing.T) {
	r := New(&config.Config{BaseURL: "http://exemplo:8080/v1", Model: "m", Verbose: true}, &agentEngine{})
	r.in = bufio.NewScanner(strings.NewReader("/sair\n"))
	out := captureStdout(t, func() { r.Run(context.Background()) })
	if !strings.Contains(out, "http://exemplo:8080/v1") {
		t.Fatalf("banner verbose deveria mostrar o servidor: %q", out)
	}
}
