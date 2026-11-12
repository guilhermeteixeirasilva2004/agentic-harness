// Package config carrega a configuração do harness a partir de flags e variáveis de ambiente.
package config

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultSystem = "Você é um assistente útil, direto e conciso. Responda em português do Brasil."

// Config reúne as opções de execução do harness.
type Config struct {
	BaseURL     string
	Model       string
	System      string
	Temperature float64
	MaxTokens   int
	Stream      bool
	// Verbose mostra as decisões e chamadas de tool do agente.
	Verbose bool

	// Controle do agente (subcomando "agent").
	MaxSteps int
	// Strategy escolhe a estratégia de raciocínio: react (padrão), cot ou plan.
	Strategy string
	// MaxContextTokens limita o histórico enviado ao modelo (0 = sem limite).
	MaxContextTokens int
	AllowTools       []string
	// EmbedModel é o modelo usado em /embeddings (base para RAG).
	EmbedModel string
	// RAG liga a busca por similaridade nas notas de memória; TopK define
	// quantas notas são injetadas no contexto quando o RAG está ativo.
	RAG  bool
	TopK int
	// Retries é o número de tentativas extras em falhas transitórias do modelo
	// (0 desativa) e Backoff é a espera inicial, que dobra a cada tentativa.
	Retries int
	Backoff time.Duration
	// AllowInsecureHTTP permite usar http:// com host remoto (o tráfego do
	// prompt e das respostas vai em claro). Sem ela, só loopback é aceito.
	AllowInsecureHTTP bool

	// Segurança de tools mutantes (subcomando "agent").
	// Approve é o modo de aprovação: ask (padrão), auto ou deny.
	Approve string
	// ShellAllow lista os prefixos de comando liberados para run_shell.
	ShellAllow []string

	// Persistência de sessões (subcomando "agent").
	StorePath string
	Session   string
	Resume    bool
	// Continue retoma a tarefa incompleta salva em checkpoint (gravado quando os
	// passos acabam sem resposta final).
	Continue bool
}

// Load lê as flags de linha de comando, usando variáveis de ambiente como padrão.
// Precedência: flag > variável de ambiente > valor padrão.
func Load(args []string) *Config {
	baseURL := envString("HARNESS_URL", "http://127.0.0.1:8080/v1")
	model := envString("HARNESS_MODEL", "local")
	system := envString("HARNESS_SYSTEM", defaultSystem)
	temp := envFloat("HARNESS_TEMP", 0.7)

	fs := flag.NewFlagSet("agentic-harness", flag.ExitOnError)
	fs.StringVar(&baseURL, "url", baseURL, "base URL do servidor compatível com OpenAI")
	allowInsecureHTTP := fs.Bool("allow-insecure-http", envBool("HARNESS_ALLOW_INSECURE_HTTP", false), "permite http:// para host remoto (tráfego em claro)")
	fs.StringVar(&model, "model", model, "nome do modelo enviado na requisição")
	fs.StringVar(&system, "system", system, "system prompt inicial")
	fs.Float64Var(&temp, "temp", temp, "temperatura de amostragem")
	maxTokens := fs.Int("maxtokens", 0, "máximo de tokens por resposta (0 = padrão do servidor)")
	stream := fs.Bool("stream", true, "usar streaming de tokens")
	verbose := fs.Bool("verbose", envBool("HARNESS_VERBOSE", false), "mostra decisões e chamadas de tool do agente")

	maxSteps := envInt("HARNESS_MAXSTEPS", 4)
	strategy := strings.ToLower(envString("HARNESS_STRATEGY", "react"))
	maxContext := envInt("HARNESS_MAXCONTEXT", 0)
	allowStr := strings.Join(envCSV("HARNESS_ALLOW", []string{"get_time", "read_file"}), ",")
	embedModel := envString("HARNESS_EMBED_MODEL", model)
	rag := envBool("HARNESS_RAG", false)
	topK := envInt("HARNESS_TOPK", 5)
	retries := envInt("HARNESS_RETRIES", 2)
	backoff := envDuration("HARNESS_BACKOFF", 500*time.Millisecond)
	storePath := envString("HARNESS_STORE", DefaultPath())
	session := envString("HARNESS_SESSION", "")
	resume := envBool("HARNESS_RESUME", false)
	continuar := envBool("HARNESS_CONTINUE", false)
	approve := NormalizeApprove(envString("HARNESS_APPROVE", "ask"))
	shellAllowStr := strings.Join(envCSV("HARNESS_SHELLALLOW", []string{"ls", "cat", "grep", "find", "head", "tail", "wc"}), ",")

	fs.IntVar(&maxSteps, "maxsteps", maxSteps, "máximo de passos do agente (subcomando agent)")
	fs.StringVar(&strategy, "strategy", strategy, "estratégia de raciocínio: react|cot|plan (subcomando agent)")
	fs.IntVar(&maxContext, "maxcontext", maxContext, "orçamento de contexto em tokens (0 = sem limite)")
	fs.StringVar(&allowStr, "allow", allowStr, "tools permitidas, separadas por vírgula (subcomando agent)")
	fs.StringVar(&embedModel, "embedmodel", embedModel, "modelo usado em /embeddings")
	fs.BoolVar(&rag, "rag", rag, "usa busca por similaridade (embeddings) nas notas de memória")
	fs.IntVar(&topK, "topk", topK, "quantas notas de memória injetar com -rag ligado")
	fs.IntVar(&retries, "retries", retries, "tentativas extras em falhas transitórias do modelo (0 desativa)")
	fs.DurationVar(&backoff, "backoff", backoff, "espera inicial entre retentativas (dobra a cada tentativa)")
	fs.StringVar(&storePath, "store", storePath, `arquivo NDJSON de sessões ("" desativa a persistência)`)
	fs.StringVar(&session, "session", session, "identificador da sessão (vazio = gerado)")
	fs.BoolVar(&resume, "resume", resume, "retoma o histórico da última sessão")
	fs.BoolVar(&continuar, "continue", continuar, "retoma a tarefa incompleta salva em checkpoint (subcomando agent)")
	fs.StringVar(&approve, "approve", approve, "aprovação de tools mutantes: ask|auto|deny (subcomando agent)")
	fs.StringVar(&shellAllowStr, "shellallow", shellAllowStr, "prefixos de comando liberados para run_shell")
	_ = fs.Parse(args)

	if topK <= 0 {
		topK = 5
	}
	if retries < 0 {
		retries = 0
	}
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}

	return &Config{
		BaseURL:           baseURL,
		Model:             model,
		System:            system,
		Temperature:       temp,
		MaxTokens:         *maxTokens,
		Stream:            *stream,
		Verbose:           *verbose,
		MaxSteps:          maxSteps,
		Strategy:          strategy,
		MaxContextTokens:  maxContext,
		AllowTools:        splitCSV(allowStr),
		EmbedModel:        embedModel,
		RAG:               rag,
		TopK:              topK,
		Retries:           retries,
		Backoff:           backoff,
		AllowInsecureHTTP: *allowInsecureHTTP,
		StorePath:         storePath,
		Session:           session,
		Resume:            resume,
		Continue:          continuar,
		Approve:           NormalizeApprove(approve),
		ShellAllow:        splitCSV(shellAllowStr),
	}
}

// NormalizeApprove valida o modo de aprovação, caindo em "ask" quando inválido.
func NormalizeApprove(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "auto":
		return "auto"
	case "deny":
		return "deny"
	default:
		return "ask"
	}
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envCSV(key string, def []string) []string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if list := splitCSV(v); len(list) > 0 {
			return list
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// splitCSV separa uma lista por vírgula, removendo espaços e itens vazios.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// DefaultPath devolve o caminho padrão do arquivo de sessões, seguindo o padrão
// XDG (~/.local/share/agentic-harness/sessions.jsonl quando XDG_DATA_HOME não
// está definido).
func DefaultPath() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "agentic-harness", "sessions.jsonl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "sessions.jsonl"
	}
	return filepath.Join(home, ".local", "share", "agentic-harness", "sessions.jsonl")
}
