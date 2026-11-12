package ui

import (
	"context"
	"fmt"
	"strings"

	"agentic-harness/internal/agent"
	"agentic-harness/internal/config"
)

// Approve implementa agent.Approver: pergunta no terminal quando o modo é "ask",
// aprova em "auto" e recusa em "deny".
func (r *REPL) Approve(_ context.Context, req agent.ApprovalRequest) (bool, error) {
	switch r.approve {
	case "auto":
		fmt.Printf("\n[aprovação automática] %s\n", req.Summary)
		return true, nil
	case "deny":
		fmt.Printf("\n[aprovação negada: modo deny] %s\n", req.Summary)
		return false, nil
	}
	fmt.Printf("\nPosso fazer isto: %s\n", req.Summary)
	fmt.Print("permitir? [s/N] ")
	if !r.in.Scan() {
		fmt.Println()
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(r.in.Text())) {
	case "s", "sim", "y", "yes":
		return true, nil
	}
	return false, nil
}

// ApproveMode devolve o modo atual de aprovação de tools mutantes.
func (r *REPL) ApproveMode() string { return r.approve }

// setApproveMode normaliza e aplica um novo modo, devolvendo o valor efetivo.
func (r *REPL) setApproveMode(mode string) string {
	r.approve = config.NormalizeApprove(mode)
	return r.approve
}
