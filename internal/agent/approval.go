package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentic-harness/internal/tools"
)

// ApprovalRequest descreve uma tool mutante que precisa de aval humano.
type ApprovalRequest struct {
	Tool      string
	Arguments json.RawMessage
	// Summary é uma descrição curta e legível (usada no prompt do terminal).
	Summary string
}

// Approver decide se uma tool mutante pode ser executada.
type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) (bool, error)
}

// SetApprover define quem aprova tools mutantes. Sem approver, tais tools são
// negadas (comportamento seguro para execuções não interativas).
func (a *Agent) SetApprover(ap Approver) { a.approver = ap }

// approveTool consulta o approver quando a tool exige aprovação. Tools somente
// leitura (ou bloqueadas pela política) passam direto: no segundo caso o
// dispatch devolve o erro da política sem incomodar o humano.
func (a *Agent) approveTool(ctx context.Context, call tools.Call) bool {
	name := strings.TrimSpace(call.Tool)
	if !a.policy.AllowTool(name) {
		return true
	}
	t, ok := a.reg.Get(name)
	if !ok || !tools.RequiresApproval(t) {
		return true
	}
	approved := false
	summary := approvalSummary(call)
	if a.approver != nil {
		ok, err := a.approver.Approve(ctx, ApprovalRequest{
			Tool:      name,
			Arguments: call.Arguments,
			Summary:   summary,
		})
		if err != nil {
			a.logf("aprovação (%s): erro %v", name, err)
		} else if ok {
			approved = true
		}
	}
	if approved {
		a.meter.RecordApproval()
	} else {
		a.meter.RecordDenial()
	}
	a.auditApproval(call, summary, approved)
	return approved
}

// approvalRecord é o payload persistido no store para auditoria das decisões.
type approvalRecord struct {
	Tool      string          `json:"tool"`
	Summary   string          `json:"summary,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Approved  bool            `json:"approved"`
}

func (a *Agent) auditApproval(call tools.Call, summary string, approved bool) {
	if a.store == nil {
		return
	}
	_ = a.store.Append("approval", a.session, approvalRecord{
		Tool:      call.Tool,
		Summary:   summary,
		Arguments: call.Arguments,
		Approved:  approved,
	})
}

// approvalSummary descreve a chamada por inteiro (tool + argumentos completos):
// o humano — e a auditoria — precisa ver exatamente o que será executado para
// decidir. Só as quebras de linha viram espaço (para não bagunçar o terminal);
// nenhum conteúdo é cortado.
func approvalSummary(call tools.Call) string {
	args := strings.TrimSpace(string(call.Arguments))
	if args == "" || args == "null" {
		return call.Tool
	}
	args = strings.Join(strings.Split(args, "\n"), " ")
	return fmt.Sprintf("%s %s", call.Tool, args)
}
