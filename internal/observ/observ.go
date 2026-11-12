// Package observ coleta métricas de execução do agente sem dependências
// externas: latência de chamadas LLM e de tools, tokens estimados e contagem de
// turnos/passos. O Meter é seguro para uso concorrente.
package observ

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ToolStat agrega as métricas de uma ferramenta específica.
type ToolStat struct {
	Calls  int           `json:"calls"`
	Errors int           `json:"errors"`
	Total  time.Duration `json:"total"`
}

// Metrics é o retrato agregado de uma sessão.
type Metrics struct {
	Turns      int                 `json:"turns"`
	Steps      int                 `json:"steps"`
	LLMCalls   int                 `json:"llm_calls"`
	Retries    int                 `json:"retries"`
	TokensIn   int                 `json:"tokens_in"`
	TokensOut  int                 `json:"tokens_out"`
	LLMTotal   time.Duration       `json:"llm_total"`
	ToolCalls  int                 `json:"tool_calls"`
	ToolErrors int                 `json:"tool_errors"`
	ToolTotal  time.Duration       `json:"tool_total"`
	Approvals  int                 `json:"approvals"`
	Denials    int                 `json:"denials"`
	WallTotal  time.Duration       `json:"wall_total"`
	PerTool    map[string]ToolStat `json:"per_tool,omitempty"`
}

// Summary descreve as métricas em texto legível (usado por /metrics e -verbose).
func (m Metrics) Summary() string {
	var b strings.Builder
	b.WriteString("métricas da sessão")
	fmt.Fprintf(&b, "\n  turnos:       %d", m.Turns)
	fmt.Fprintf(&b, "\n  passos:       %d", m.Steps)
	fmt.Fprintf(&b, "\n  chamadas LLM: %d (~%s por chamada)", m.LLMCalls, avg(m.LLMTotal, m.LLMCalls))
	if m.Retries > 0 {
		fmt.Fprintf(&b, "\n  retentativas: %d", m.Retries)
	}
	fmt.Fprintf(&b, "\n  tokens:       entrada ~%d, saída ~%d", m.TokensIn, m.TokensOut)
	fmt.Fprintf(&b, "\n  latência:     LLM %s, tools %s, parede %s", ms(m.LLMTotal), ms(m.ToolTotal), ms(m.WallTotal))
	fmt.Fprintf(&b, "\n  tools:        %d chamada(s), %d erro(s)", m.ToolCalls, m.ToolErrors)
	fmt.Fprintf(&b, "\n  aprovação:    %d aprovada(s), %d negada(s)", m.Approvals, m.Denials)
	if len(m.PerTool) > 0 {
		names := make([]string, 0, len(m.PerTool))
		for n := range m.PerTool {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			st := m.PerTool[n]
			fmt.Fprintf(&b, "\n    %-12s %d chamada(s), %d erro(s), %s", n, st.Calls, st.Errors, ms(st.Total))
		}
	}
	return b.String()
}

// Meter acumula as métricas de uma sessão de forma concorrente-segura.
type Meter struct {
	mu sync.Mutex
	m  Metrics
}

// New cria um Meter zerado.
func New() *Meter {
	return &Meter{m: Metrics{PerTool: make(map[string]ToolStat)}}
}

// RecordLLM contabiliza uma chamada ao modelo e sua latência.
func (m *Meter) RecordLLM(d time.Duration) {
	m.mu.Lock()
	m.m.LLMCalls++
	m.m.LLMTotal += d
	m.mu.Unlock()
}

// RecordRetry contabiliza uma nova tentativa após uma falha transitória.
func (m *Meter) RecordRetry() {
	m.mu.Lock()
	m.m.Retries++
	m.mu.Unlock()
}

// RecordTokens soma os tokens estimados de entrada e saída de uma chamada.
func (m *Meter) RecordTokens(in, out int) {
	m.mu.Lock()
	m.m.TokensIn += in
	m.m.TokensOut += out
	m.mu.Unlock()
}

// RecordStep contabiliza uma iteração do loop agêntico.
func (m *Meter) RecordStep() {
	m.mu.Lock()
	m.m.Steps++
	m.mu.Unlock()
}

// RecordTurn contabiliza um turno concluído e seu tempo de parede.
func (m *Meter) RecordTurn(d time.Duration) {
	m.mu.Lock()
	m.m.Turns++
	m.m.WallTotal += d
	m.mu.Unlock()
}

// RecordTool contabiliza uma chamada de tool, sua latência e eventual erro.
func (m *Meter) RecordTool(name string, d time.Duration, err error) {
	if strings.TrimSpace(name) == "" {
		name = "(desconhecida)"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m.ToolCalls++
	m.m.ToolTotal += d
	st := m.m.PerTool[name]
	st.Calls++
	st.Total += d
	if err != nil {
		m.m.ToolErrors++
		st.Errors++
	}
	m.m.PerTool[name] = st
}

// RecordApproval contabiliza uma tool mutante aprovada.
func (m *Meter) RecordApproval() {
	m.mu.Lock()
	m.m.Approvals++
	m.mu.Unlock()
}

// RecordDenial contabiliza uma tool mutante negada.
func (m *Meter) RecordDenial() {
	m.mu.Lock()
	m.m.Denials++
	m.mu.Unlock()
}

// Snapshot devolve uma cópia consistente das métricas até o momento.
func (m *Meter) Snapshot() Metrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.m
	s.PerTool = make(map[string]ToolStat, len(m.m.PerTool))
	for k, v := range m.m.PerTool {
		s.PerTool[k] = v
	}
	return s
}

func ms(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d.Round(time.Millisecond)
}

func avg(total time.Duration, n int) time.Duration {
	if n <= 0 {
		return 0
	}
	return (total / time.Duration(n)).Round(time.Millisecond)
}
