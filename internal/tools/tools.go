// Package tools define o contrato de ferramentas que o agente pode chamar e um
// registry com execução segura: allowlist, timeout e validação de argumentos.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentic-harness/internal/textutil"
)

// ErrNotAllowed indica que a tool não está na allowlist.
var ErrNotAllowed = errors.New("tool não permitida")

// Tool é uma capacidade que o modelo pode solicitar.
type Tool interface {
	// Name é o identificador usado pelo modelo (ex.: "read_file").
	Name() string
	// Description explica quando usar a tool (vai no prompt).
	Description() string
	// Parameters devolve o JSON Schema dos argumentos (sempre um objeto).
	Parameters() map[string]any
	// Run executa a tool com os argumentos já decodificados.
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Guarded é implementada por tools que alteram o sistema (arquivos, comandos) e
// portanto exigem aprovação humana antes de executar.
type Guarded interface {
	RequiresApproval() bool
}

// RequiresApproval informa se a tool exige aprovação humana (falso por padrão).
func RequiresApproval(t Tool) bool {
	g, ok := t.(Guarded)
	return ok && g.RequiresApproval()
}

// Call é a solicitação de tool devolvida pelo modelo.
type Call struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// ParseCall extrai um Call de uma saída do modelo, tolerando cercas de código
// ```json e texto ao redor do objeto.
func ParseCall(s string) (Call, error) {
	var call Call
	s = stripFences(s)
	obj, ok := extractObject(s)
	if !ok {
		return call, fmt.Errorf("saída não contém um objeto JSON: %q", truncate(s, 120))
	}
	if err := json.Unmarshal([]byte(obj), &call); err != nil {
		return call, fmt.Errorf("JSON inválido: %w", err)
	}
	if strings.TrimSpace(call.Tool) == "" {
		return call, errors.New(`chamada sem campo "tool"`)
	}
	return call, nil
}

// ExtractObject devolve o primeiro objeto JSON balanceado encontrado em s,
// ignorando cercas de código ```json e texto ao redor.
func ExtractObject(s string) (string, bool) {
	return extractObject(stripFences(s))
}

// extractObject devolve o primeiro objeto JSON balanceado encontrado em s.
func extractObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	for {
		i := strings.Index(s, "```")
		if i < 0 {
			return s
		}
		rest := s[i+3:]
		if j := strings.IndexByte(rest, '\n'); j >= 0 {
			rest = rest[j+1:]
		}
		if k := strings.Index(rest, "```"); k >= 0 {
			rest = rest[:k]
		}
		s = strings.TrimSpace(rest)
	}
}

func truncate(s string, n int) string {
	return textutil.TruncateRunes(s, n)
}

// Registry agrupa tools e aplica a allowlist antes de executar.
type Registry struct {
	tools   map[string]Tool
	ordered []string
	allow   map[string]bool
	timeout time.Duration
}

// NewRegistry cria um registry vazio com timeout padrão de execução.
func NewRegistry() *Registry {
	return &Registry{
		tools:   make(map[string]Tool),
		allow:   make(map[string]bool),
		timeout: 10 * time.Second,
	}
}

// Add registra uma tool.
func (r *Registry) Add(t Tool) {
	name := t.Name()
	if _, ok := r.tools[name]; !ok {
		r.ordered = append(r.ordered, name)
	}
	r.tools[name] = t
}

// Allow define a allowlist de execução. Nomes fora dela são recusados.
func (r *Registry) Allow(names ...string) {
	r.allow = make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" {
			r.allow[n] = true
		}
	}
}

// IsAllowed informa se a tool pode ser executada.
func (r *Registry) IsAllowed(name string) bool { return r.allow[name] }

// Get devolve uma tool registrada.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names devolve os nomes registrados em ordem alfabética estável.
func (r *Registry) Names() []string {
	out := make([]string, len(r.ordered))
	copy(out, r.ordered)
	sort.Strings(out)
	return out
}

// Catalog descreve as tools permitidas para o prompt do modelo.
func (r *Registry) Catalog() string {
	var b strings.Builder
	for _, name := range r.Names() {
		if !r.IsAllowed(name) {
			continue
		}
		t := r.tools[name]
		fmt.Fprintf(&b, "- %s: %s\n", t.Name(), t.Description())
		if params, err := json.Marshal(t.Parameters()); err == nil {
			fmt.Fprintf(&b, "  argumentos (JSON Schema): %s\n", params)
		}
		if req := requiredFields(t); len(req) > 0 {
			fmt.Fprintf(&b, "  obrigatórios: %s\n", strings.Join(req, ", "))
		} else {
			b.WriteString("  obrigatórios: nenhum\n")
		}
	}
	return b.String()
}

// ValidateArgs confere os campos obrigatórios declarados no schema da tool.
// É uma checagem rasa (presença), suficiente para o tool calling do agente.
func ValidateArgs(t Tool, args json.RawMessage) error {
	required := requiredFields(t)
	if len(required) == 0 {
		return nil
	}
	got := map[string]json.RawMessage{}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &got); err != nil {
			return fmt.Errorf("arguments deve ser um objeto JSON: %w", err)
		}
	}
	missing := make([]string, 0, len(required))
	for _, name := range required {
		if _, ok := got[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("argumentos obrigatórios ausentes: %s", strings.Join(missing, ", "))
	}
	return nil
}

// requiredFields lê a lista "required" do schema de parâmetros da tool.
func requiredFields(t Tool) []string {
	raw, ok := t.Parameters()["required"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Dispatch valida a allowlist e executa a tool com timeout.
func (r *Registry) Dispatch(ctx context.Context, call Call) (string, error) {
	name := strings.TrimSpace(call.Tool)
	if name == "" {
		return "", errors.New("chamada sem nome de tool")
	}
	if !r.IsAllowed(name) {
		return "", fmt.Errorf("%w: %s", ErrNotAllowed, name)
	}
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("tool desconhecida: %s", name)
	}
	args := call.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	if err := ValidateArgs(t, args); err != nil {
		return err.Error(), err
	}
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := t.Run(runCtx, args)
	if err != nil {
		return err.Error(), err
	}
	return out, nil
}
