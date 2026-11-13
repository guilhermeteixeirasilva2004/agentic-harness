// Package agent implementa o loop ReAct (pensar → agir → observar) com tool
// calling orquestrado em Go: o modelo devolve JSON estruturado (a decisão e,
// quando precisa de tool, os argumentos) e o Go valida, executa e realimenta a
// observação.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"agentic-harness/internal/cognition"
	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/observ"
	"agentic-harness/internal/present"
	"agentic-harness/internal/reasoning"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/store"
	"agentic-harness/internal/textutil"
	"agentic-harness/internal/tools"
)

// LLM é o subconjunto do cliente usado pelo loop (permite testes com fake).
type LLM interface {
	Complete(ctx context.Context, req llm.ChatRequest) (string, error)
}

// Streamer é implementado por clientes que sabem transmitir a resposta token a
// token. O loop usa streaming apenas na resposta final; as decisões continuam
// sendo pedidas sem stream e com json_schema.
type Streamer interface {
	Chat(ctx context.Context, req llm.ChatRequest, onDelta func(string)) (string, error)
}

// Agent conduz o loop agêntico de uma conversa.
type Agent struct {
	cfg      *config.Config
	llm      LLM
	reg      *tools.Registry
	policy   *safety.Policy
	strategy reasoning.Strategy
	steps    *cognition.Trace
	history  []llm.Message
	// memory é o resumo acumulado das mensagens descartadas por -maxcontext.
	memory []string
	// notes são fatos persistentes (registros "memory" do store).
	notes           []string
	trace           io.Writer
	meter           *observ.Meter
	store           *store.Store
	session         string
	sessionExplicit bool
	approver        Approver
	// asker é o canal de interação com o humano (tool ask_user).
	asker Asker
	// embed + noteVecs implementam o RAG leve nas notas de memória; selected
	// guarda as notas escolhidas por similaridade no turno atual.
	embed    Embedder
	noteVecs map[string][]float32
	selected []string
}

// New cria um agente com as dependências informadas. A estratégia de raciocínio
// vem de cfg.Strategy (react quando vazio).
func New(cfg *config.Config, client LLM, reg *tools.Registry, policy *safety.Policy) *Agent {
	a := &Agent{
		cfg:      cfg,
		llm:      client,
		reg:      reg,
		policy:   policy,
		strategy: reasoning.Get(cfg.Strategy),
		steps:    cognition.New(newTraceID()),
		meter:    observ.New(),
		session:  newSessionID(),
	}
	a.rebuild()
	return a
}

// NewDefault monta o agente com as tools embutidas e a allowlist da config.
func NewDefault(cfg *config.Config) *Agent {
	cwd, _ := os.Getwd()
	reg := tools.NewRegistry()
	reg.Add(tools.GetTime{})
	reg.Add(tools.ReadFile{Root: cwd})
	reg.Add(tools.ListDir{Root: cwd})
	reg.Add(tools.WriteFile{Root: cwd})
	reg.Add(tools.EditFile{Root: cwd})
	reg.Add(tools.DeleteFile{Root: cwd})
	reg.Add(tools.MoveFile{Root: cwd})
	reg.Add(tools.Mkdir{Root: cwd})
	jobs := tools.NewJobs()
	reg.Add(tools.RunShell{Root: cwd, Allow: cfg.ShellAllow})
	reg.Add(tools.RunShellBG{Root: cwd, Allow: cfg.ShellAllow, Jobs: jobs})
	reg.Add(tools.Wait{Jobs: jobs})
	reg.Allow(cfg.AllowTools...)
	client := llm.NewClient(cfg.BaseURL)
	a := New(cfg, client, reg, safety.New(cfg.MaxSteps, cfg.AllowTools))
	a.SetEmbedder(client)
	reg.Add(rememberTool{a: a})
	reg.Add(askUserTool{a: a})
	a.rebuild()
	if cfg.Verbose {
		a.SetTrace(os.Stderr)
		a.logf("estratégia: %s", a.strategy.Name())
	}
	return a
}

func (a *Agent) rebuild() {
	a.history = a.history[:0]
	a.history = append(a.history, llm.Message{Role: llm.RoleSystem, Content: a.systemPrompt()})
	a.refreshMemory()
}

func (a *Agent) systemPrompt() string {
	base := strings.TrimSpace(a.cfg.System)
	if base == "" {
		base = "Você é um assistente útil, direto e conciso. Responda em português do Brasil."
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nData e hora atuais: ")
	b.WriteString(time.Now().Format("02/01/2006 15:04"))
	b.WriteString(". Use essa data em contas de tempo, idade ou prazo.\n")
	b.WriteString("\nVocê resolve tarefas em passos e pode usar ferramentas. A cada passo responda APENAS com um objeto JSON, sem texto fora dele:\n")
	b.WriteString(a.strategy.Instructions())
	b.WriteString("\n\n")
	if start := strings.TrimSpace(a.strategy.Start()); start != "" {
		b.WriteString(start)
		b.WriteString("\n\n")
	}
	b.WriteString("Os argumentos da tool são preenchidos em uma etapa seguinte, validados e executados pelo harness.\n\n")
	b.WriteString("Ferramentas permitidas:\n")
	catalog := strings.TrimSpace(a.reg.Catalog())
	if catalog == "" {
		catalog = "(nenhuma)"
	}
	b.WriteString(catalog)
	b.WriteString("\n\nRegras: use no máximo ")
	b.WriteString(fmt.Sprintf("%d passos", a.cfg.MaxSteps))
	b.WriteString(". Não invente observações; use apenas os resultados das ferramentas. " +
		"Se uma tool retornar ERRO, considere o erro ao escolher a próxima ação. " +
		"Se não precisar de ferramenta, responda com action \"final\".\n")
	return b.String()
}

// Reply implementa ui.Engine.
func (a *Agent) Reply(ctx context.Context, input string, onDelta func(string)) (string, error) {
	return a.Turn(ctx, input, onDelta)
}

// SetSystem atualiza o system prompt e reinicia o histórico.
func (a *Agent) SetSystem(system string) {
	a.cfg.System = system
	a.rebuild()
}

// Clear reinicia o histórico e o resumo acumulado, preservando o system prompt
// e as notas persistentes de longo prazo.
func (a *Agent) Clear() {
	a.steps = cognition.New(newTraceID())
	a.memory = nil
	a.rebuild()
}

// SetStrategy troca a estratégia de raciocínio e reinicia o histórico.
func (a *Agent) SetStrategy(s reasoning.Strategy) {
	if s == nil {
		s = reasoning.Get("")
	}
	a.strategy = s
	a.steps = cognition.New(newTraceID())
	a.rebuild()
}

// Strategy devolve o nome da estratégia de raciocínio em uso.
func (a *Agent) Strategy() string { return a.strategy.Name() }

// Trace devolve o trace estruturado da conversa em texto legível.
func (a *Agent) Trace() string { return a.steps.RenderMarkdown() }

// SetTrace ativa o rastreamento de decisões e chamadas de tool.
func (a *Agent) SetTrace(w io.Writer) { a.trace = w }

// SetStore conecta o agente a um arquivo de sessões NDJSON.
func (a *Agent) SetStore(s *store.Store) { a.store = s }

// SetSession define o identificador da sessão (vazio mantém o gerado).
func (a *Agent) SetSession(id string) {
	if id = strings.TrimSpace(id); id != "" {
		a.session = id
		a.sessionExplicit = true
	}
}

// SessionID devolve o identificador da sessão atual.
func (a *Agent) SessionID() string { return a.session }

// Metrics devolve o resumo das métricas acumuladas da sessão.
func (a *Agent) Metrics() string { return a.meter.Snapshot().Summary() }

// History devolve o histórico da conversa em texto legível.
func (a *Agent) History() string { return present.History(a.history) }

// Resume restaura o histórico de uma sessão persistida. Sem sessão explícita,
// retoma a mais recente. Exige um store conectado.
func (a *Agent) Resume() error {
	if a.store == nil {
		return fmt.Errorf("persistência desativada")
	}
	recs, err := a.store.Query()
	if err != nil {
		return err
	}
	// As notas de longo prazo valem para qualquer sessão retomada.
	a.applyNotes(recs)
	target := a.session
	if !a.sessionExplicit {
		target = lastSession(recs)
		if target == "" {
			return fmt.Errorf("nenhuma sessão persistida")
		}
	}
	a.rebuild()
	found := false
	for _, r := range recs {
		if r.Type != "turn" || r.Session != target {
			continue
		}
		var t turnRecord
		if err := json.Unmarshal(r.Payload, &t); err != nil {
			continue
		}
		if in := strings.TrimSpace(t.Input); in != "" {
			a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: in})
		}
		if out := strings.TrimSpace(t.Output); out != "" {
			a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: out})
		}
		found = true
	}
	if !found {
		return fmt.Errorf("sessão %q não encontrada", target)
	}
	a.session = target
	a.sessionExplicit = false
	return nil
}

// Sessions lista as sessões persistidas, da mais recente para a mais antiga.
func (a *Agent) Sessions() string {
	if a.store == nil {
		return "(persistência desativada; rode com -store <arquivo>)"
	}
	recs, err := a.store.Query()
	if err != nil {
		return "erro ao ler sessões: " + err.Error()
	}
	return present.Sessions(a.store.Path(), recs, a.session)
}

// ShowSession mostra os turnos de uma sessão persistida (vazio = sessão atual).
func (a *Agent) ShowSession(id string) string {
	if a.store == nil {
		return "(persistência desativada; rode com -store <arquivo>)"
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = a.session
	}
	recs, err := a.store.Query()
	if err != nil {
		return "erro ao ler sessões: " + err.Error()
	}
	return present.ShowSession(recs, id)
}

// turnRecord é o payload persistido ao fim de cada turno.
type turnRecord struct {
	Input    string           `json:"input"`
	Output   string           `json:"output,omitempty"`
	Error    string           `json:"error,omitempty"`
	Strategy string           `json:"strategy"`
	Model    string           `json:"model"`
	Duration time.Duration    `json:"duration"`
	Steps    []cognition.Step `json:"steps,omitempty"`
	Metrics  observ.Metrics   `json:"metrics"`
}

// persistTurn grava o turno no store, se houver um conectado. Só persiste as
// etapas do trace acrescentadas nesta rodada (índice before em diante).
func (a *Agent) persistTurn(input, output string, runErr error, elapsed time.Duration, before int) {
	if a.store == nil {
		return
	}
	var steps []cognition.Step
	if before >= 0 && before <= len(a.steps.Steps) {
		steps = a.steps.Steps[before:]
	}
	rec := turnRecord{
		Input:    input,
		Output:   output,
		Strategy: a.strategy.Name(),
		Model:    a.cfg.Model,
		Duration: elapsed,
		Steps:    steps,
		Metrics:  a.meter.Snapshot(),
	}
	if runErr != nil {
		rec.Error = runErr.Error()
	}
	if err := a.store.Append("turn", a.session, rec); err != nil {
		a.logf("falha ao persistir turno: %v", err)
	}
}

// lastSession devolve a sessão do registro mais recente.
func lastSession(recs []store.Record) string {
	last := ""
	var at time.Time
	for _, r := range recs {
		if r.Session == "" {
			continue
		}
		if last == "" || r.At.After(at) {
			last, at = r.Session, r.At
		}
	}
	return last
}

// historyTokens estima os tokens de entrada de uma lista de mensagens.
func historyTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += cognition.EstimateTokens(m.Content)
	}
	return total
}

func (a *Agent) logf(format string, args ...any) {
	if a.trace == nil {
		return
	}
	fmt.Fprintf(a.trace, "[agente] "+format+"\n", args...)
}

func truncateForLog(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return textutil.TruncateRunes(s, n)
}

// Turn executa uma rodada do loop agêntico, registrando métricas e persistindo
// o turno quando há um store conectado. Grava apenas as etapas do trace
// acrescentadas nesta rodada.
func (a *Agent) Turn(ctx context.Context, input string, onDelta func(string)) (string, error) {
	started := time.Now()
	before := a.steps.Len()
	out, err := a.turn(ctx, input, onDelta)
	elapsed := time.Since(started)
	a.meter.RecordTurn(elapsed)
	a.persistTurn(input, out, err, elapsed, before)
	return out, err
}

func (a *Agent) turn(ctx context.Context, input string, onDelta func(string)) (string, error) {
	if err := a.policy.CheckInput(input); err != nil {
		return "", err
	}
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: input})
	a.retrieveNotes(ctx, input)
	a.trimHistory()

	for step := 0; a.policy.StepsOK(step); step++ {
		a.meter.RecordStep()
		promptTokens := historyTokens(a.history)
		raw, err := a.complete(ctx, llm.ChatRequest{
			Model:       a.cfg.Model,
			Messages:    a.history,
			Temperature: a.cfg.Temperature,
			MaxTokens:   a.cfg.MaxTokens,
			ResponseFormat: &llm.ResponseFormat{
				Type:       "json_schema",
				JSONSchema: &llm.JSONSchema{Name: "decision", Schema: a.decisionSchema()},
			},
		})
		a.meter.RecordTokens(promptTokens, cognition.EstimateTokens(raw))
		if err != nil {
			return "", err
		}

		a.logf("resposta: %s", truncateForLog(raw, 240))
		d, err := parseDecision(raw)
		if err != nil {
			// Saída fora do contrato: devolve como resposta final para não travar.
			answer := fallbackAnswer(raw)
			a.record(cognition.Step{Kind: cognition.KindFinal, Answer: answer})
			return a.finish(answer, onDelta), nil
		}

		if r := strings.TrimSpace(d.Reasoning); r != "" {
			a.record(cognition.Step{Kind: cognition.KindThought, Reasoning: r})
			a.logf("pensamento: %s", truncateForLog(r, 200))
		}

		switch d.Action {
		case "final":
			a.record(cognition.Step{Kind: cognition.KindFinal, Action: d.Action, Answer: d.Answer})
			a.logf("decisão: final")
			return a.finishFinal(ctx, d.Answer, onDelta), nil
		case "plan", "replan":
			if len(d.Plan) == 0 {
				msg := fmt.Sprintf("ERRO (plano): a ação %q exige o campo \"plan\" com ao menos uma etapa.", d.Action)
				a.record(cognition.Step{Kind: cognition.KindError, Observation: msg})
				a.logf("%s", msg)
				a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: msg})
				continue
			}
			a.record(cognition.Step{Kind: cognition.KindPlan, Action: d.Action, Plan: d.Plan})
			a.logf("plano (%s): %s", d.Action, strings.Join(d.Plan, " | "))
			a.setPlan(d.Plan)
		case "tool":
			if strings.TrimSpace(d.Tool) == "" {
				msg := "ERRO: a ação \"tool\" exige o campo \"tool\" com o nome da ferramenta."
				a.record(cognition.Step{Kind: cognition.KindError, Action: d.Action, Observation: msg})
				a.logf("%s", msg)
				a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: msg})
				continue
			}
			call := tools.Call{Tool: d.Tool, Arguments: d.Arguments}
			if len(call.Arguments) == 0 || string(call.Arguments) == "null" {
				if a.policy.AllowTool(call.Tool) {
					filled, ferr := a.fillArgs(ctx, call.Tool, input)
					if ferr != nil {
						if ctx.Err() != nil {
							return "", ctx.Err()
						}
						// Sem argumentos a tool rodaria vazia e o erro real
						// (permissão, schema, resposta inválida) se perderia:
						// devolve a causa ao modelo e não executa nada.
						msg := fmt.Sprintf("ERRO (args): não foi possível montar os argumentos de %s: %v", call.Tool, ferr)
						a.record(cognition.Step{Kind: cognition.KindError, Action: d.Action, Tool: call.Tool, Observation: msg})
						a.logf("%s", msg)
						a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: raw})
						a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: msg})
						continue
					}
					call.Arguments = filled
				}
			}
			a.record(cognition.Step{
				Kind:      cognition.KindToolCall,
				Action:    d.Action,
				Tool:      call.Tool,
				Arguments: call.Arguments,
			})
			a.logf("decisão: tool=%s args=%s", call.Tool, strings.TrimSpace(string(call.Arguments)))
			a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: raw})
			if !a.approveTool(ctx, call) {
				msg := fmt.Sprintf("APROVAÇÃO NEGADA (%s): a ferramenta não foi executada.", call.Tool)
				a.record(cognition.Step{Kind: cognition.KindError, Tool: call.Tool, Observation: msg})
				a.logf("%s", msg)
				a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: msg})
				continue
			}
			toolStarted := time.Now()
			obs, runErr := a.dispatch(ctx, call)
			a.meter.RecordTool(call.Tool, time.Since(toolStarted), runErr)
			label, kind := "OBSERVAÇÃO", cognition.KindObservation
			if runErr != nil {
				label, kind = "ERRO", cognition.KindError
			}
			a.record(cognition.Step{Kind: kind, Tool: call.Tool, Observation: obs})
			a.logf("%s (%s): %s", label, call.Tool, truncateForLog(obs, 160))
			a.history = append(a.history, llm.Message{
				Role:    llm.RoleUser,
				Content: fmt.Sprintf("%s (%s): %s", label, call.Tool, obs),
			})
		default:
			return "", fmt.Errorf("ação inválida devolvida pelo modelo: %q", d.Action)
		}
	}
	// Os passos acabaram sem resposta final: salva o estado da tarefa para
	// permitir retomada em outro processo (-continue).
	a.persistCheckpoint(input)
	return "", fmt.Errorf("limite de %d passos atingido sem resposta final (envie /continue para prosseguir de onde parou)", a.policy.MaxSteps)
}

// record acrescenta uma etapa ao trace cognitivo.
func (a *Agent) record(s cognition.Step) { a.steps.Append(s) }

// decisionSchema escolhe o schema conforme a estratégia e o estágio atual. Com
// plano persistente, o primeiro passo só propõe o plano e os seguintes só
// executam/revisam/concluem — evita o loop de replanejamento.
func (a *Agent) decisionSchema() map[string]any {
	if planner, ok := a.strategy.(reasoning.Planner); ok {
		if len(a.steps.Plan()) == 0 {
			return planner.PlanSchema()
		}
		return planner.StepSchema()
	}
	return a.strategy.Schema()
}

const planPrefix = "PLANO ATUAL:\n"

// setPlan substitui o plano ativo no histórico do modelo (usado pela
// estratégia "plan").
func (a *Agent) setPlan(plan []string) {
	kept := a.history[:0]
	for _, m := range a.history {
		if !strings.HasPrefix(m.Content, planPrefix) {
			kept = append(kept, m)
		}
	}
	a.history = kept
	var b strings.Builder
	b.WriteString(planPrefix)
	for i, p := range plan {
		fmt.Fprintf(&b, "%d) %s\n", i+1, p)
	}
	b.WriteString("Siga o plano: execute cada etapa com action \"tool\" e a ferramenta adequada. ")
	b.WriteString("Use \"replan\" só se precisar revisar o plano e \"final\" ao concluir.")
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: b.String()})
}

// trimHistory descarta as mensagens mais antigas quando o histórico excede o
// orçamento de contexto (-maxcontext), preservando o system prompt, o plano, a
// memória e a última mensagem. O que é descartado vira resumo na memória de
// longo prazo, em vez de se perder. Sem orçamento (0), não faz nada.
func (a *Agent) trimHistory() {
	max := a.cfg.MaxContextTokens
	if max <= 0 || len(a.history) <= 2 {
		return
	}
	weights := make([]int, len(a.history))
	total := 0
	for i, m := range a.history {
		weights[i] = cognition.EstimateTokens(m.Content)
		total += weights[i]
	}
	excess := total - max
	if excess <= 0 {
		return
	}
	keep := make([]llm.Message, 0, len(a.history))
	var dropped []llm.Message
	keep = append(keep, a.history[0]) // system prompt
	for i, m := range a.history[1:] {
		idx := i + 1
		if excess > 0 && idx < len(a.history)-1 && !isPinned(m) {
			excess -= weights[idx]
			dropped = append(dropped, m)
			continue
		}
		keep = append(keep, m)
	}
	a.history = keep
	if len(dropped) > 0 {
		a.remember(dropped)
	}
}

// isPinned informa se a mensagem não pode ser descartada pelo trimHistory.
func isPinned(m llm.Message) bool {
	return strings.HasPrefix(m.Content, planPrefix) ||
		strings.HasPrefix(m.Content, memoryPrefix) ||
		strings.HasPrefix(m.Content, notesPrefix)
}

func newTraceID() string {
	return fmt.Sprintf("trace-%d", time.Now().UnixNano())
}

func newSessionID() string {
	return fmt.Sprintf("sess-%d", time.Now().UnixNano())
}

func (a *Agent) dispatch(ctx context.Context, call tools.Call) (string, error) {
	if err := a.policy.CheckTool(call.Tool); err != nil {
		return err.Error(), err
	}
	return a.reg.Dispatch(ctx, call)
}

// fillArgs preenche os argumentos da tool com uma segunda chamada guiada pelo
// JSON Schema da própria tool — o modelo só consegue devolver um objeto válido.
func (a *Agent) fillArgs(ctx context.Context, name, task string) (json.RawMessage, error) {
	t, ok := a.reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool desconhecida: %s", name)
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "Você preenche os argumentos de uma ferramenta. Responda apenas com o objeto JSON de argumentos, sem texto fora dele."},
		{Role: llm.RoleUser, Content: fmt.Sprintf("Ferramenta: %s\nDescrição: %s\nTarefa do usuário: %s", t.Name(), t.Description(), task)},
	}
	promptTokens := historyTokens(msgs)
	raw, err := a.complete(ctx, llm.ChatRequest{
		Model:       a.cfg.Model,
		Temperature: 0,
		MaxTokens:   a.cfg.MaxTokens,
		Messages:    msgs,
		ResponseFormat: &llm.ResponseFormat{
			Type:       "json_schema",
			JSONSchema: &llm.JSONSchema{Name: "arguments", Schema: t.Parameters()},
		},
	})
	a.meter.RecordTokens(promptTokens, cognition.EstimateTokens(raw))
	if err != nil {
		return nil, err
	}
	if obj, ok := tools.ExtractObject(raw); ok {
		return json.RawMessage(obj), nil
	}
	return nil, fmt.Errorf("argumentos inválidos: %q", truncateForLog(raw, 120))
}

// finalInstruction orienta a chamada streaming que produz a resposta final em
// texto corrido (sem o JSON de decisão).
const finalInstruction = "Responda agora ao usuário em texto corrido, sem JSON e sem repetir as ferramentas: entregue a resposta final, direta e completa."

// finishFinal encerra o turno com a resposta final. Com -stream ligado e um
// Streamer disponível, a resposta é gerada em streaming (token a token) para
// aparecer ao vivo no terminal; qualquer falha ou texto vazio cai na resposta
// já produzida na decisão JSON.
func (a *Agent) finishFinal(ctx context.Context, answer string, onDelta func(string)) string {
	if !a.cfg.Stream || onDelta == nil {
		return a.finish(answer, onDelta)
	}
	streamer, ok := a.llm.(Streamer)
	if !ok {
		return a.finish(answer, onDelta)
	}
	streamed, err := a.streamFinal(ctx, streamer, onDelta)
	if err != nil {
		a.logf("streaming da resposta final falhou: %v", err)
		return a.finish(answer, onDelta)
	}
	streamed = strings.TrimSpace(streamed)
	if streamed == "" {
		return a.finish(answer, onDelta)
	}
	// Os pedaços já foram emitidos via onDelta; evita duplicar a resposta.
	return a.finish(streamed, nil)
}

// streamFinal pede a resposta final em texto corrido e repassa cada pedaço ao
// terminal. Registra latência e tokens como qualquer chamada ao modelo.
func (a *Agent) streamFinal(ctx context.Context, s Streamer, onDelta func(string)) (string, error) {
	msgs := append([]llm.Message{}, a.history...)
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: finalInstruction})
	req := llm.ChatRequest{
		Model:       a.cfg.Model,
		Messages:    msgs,
		Temperature: a.cfg.Temperature,
		MaxTokens:   a.cfg.MaxTokens,
	}
	promptTokens := historyTokens(msgs)
	started := time.Now()
	out, err := s.Chat(ctx, req, onDelta)
	a.meter.RecordLLM(time.Since(started))
	a.meter.RecordTokens(promptTokens, cognition.EstimateTokens(out))
	return out, err
}

func (a *Agent) finish(answer string, onDelta func(string)) string {
	answer = a.policy.TruncateOutput(strings.TrimSpace(answer))
	if answer == "" {
		answer = "(o modelo não produziu resposta)"
	}
	a.history = append(a.history, llm.Message{Role: llm.RoleAssistant, Content: answer})
	if onDelta != nil {
		onDelta(answer)
	}
	return answer
}

// parseDecision interpreta a saída do modelo como um objeto de decisão,
// aceitando também o formato puro de tool call ({"tool":...,"arguments":...}).
func parseDecision(raw string) (reasoning.Decision, error) {
	if obj, ok := tools.ExtractObject(raw); ok {
		var d reasoning.Decision
		if err := json.Unmarshal([]byte(obj), &d); err == nil && strings.TrimSpace(d.Action) != "" {
			return d, nil
		}
	}
	if call, err := tools.ParseCall(raw); err == nil {
		return reasoning.Decision{Action: "tool", Tool: call.Tool, Arguments: call.Arguments}, nil
	}
	return reasoning.Decision{}, fmt.Errorf("decisão inválida: %q", fallbackAnswer(raw))
}

func fallbackAnswer(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "(o modelo não produziu resposta)"
	}
	return s
}
