package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentic-harness/internal/textutil"
)

// WriteFile cria ou sobrescreve um arquivo de texto dentro da raiz permitida.
// É uma tool mutante: exige aprovação humana.
type WriteFile struct {
	Root     string // raiz permitida (vazio = diretório atual)
	MaxBytes int    // limite de conteúdo (0 = 1 MiB)
}

func (WriteFile) Name() string { return "write_file" }
func (WriteFile) Description() string {
	return "Cria ou sobrescreve um arquivo de texto dentro do projeto (exige aprovação)."
}

func (WriteFile) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string", "description": "caminho relativo à raiz do projeto"},
			"content":   map[string]any{"type": "string", "description": "conteúdo completo do arquivo"},
			"overwrite": map[string]any{"type": "boolean", "description": "permite sobrescrever arquivo existente"},
		},
		"required": []string{"path", "content"},
	}
}

func (WriteFile) RequiresApproval() bool { return true }

func (t WriteFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", errors.New(`argumento "path" é obrigatório`)
	}
	limit := t.MaxBytes
	if limit <= 0 {
		limit = 1024 * 1024
	}
	if len(in.Content) > limit {
		return "", fmt.Errorf("conteúdo excede o limite de %d bytes", limit)
	}
	full, err := resolveWithin(t.Root, in.Path)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(full); statErr == nil {
		if !in.Overwrite {
			return "", fmt.Errorf("arquivo já existe (use overwrite=true para substituir): %s", in.Path)
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if dir := filepath.Dir(full); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(full, []byte(in.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("arquivo gravado: %s (%d bytes)", in.Path, len(in.Content)), nil
}

// EditFile substitui um trecho exato de um arquivo existente. Exige aprovação.
type EditFile struct {
	Root string // raiz permitida (vazio = diretório atual)
}

func (EditFile) Name() string { return "edit_file" }
func (EditFile) Description() string {
	return "Substitui a primeira ocorrência exata de um trecho em um arquivo do projeto (exige aprovação)."
}

func (EditFile) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "caminho relativo à raiz do projeto"},
			"old":  map[string]any{"type": "string", "description": "trecho exato a substituir"},
			"new":  map[string]any{"type": "string", "description": "novo trecho"},
		},
		"required": []string{"path", "old", "new"},
	}
}

func (EditFile) RequiresApproval() bool { return true }

func (t EditFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", errors.New(`argumento "path" é obrigatório`)
	}
	if in.Old == "" {
		return "", errors.New(`argumento "old" é obrigatório`)
	}
	full, err := resolveWithin(t.Root, in.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("caminho é um diretório: %s", in.Path)
	}
	if info.Size() > 4*1024*1024 {
		return "", fmt.Errorf("arquivo grande demais para edição (%d bytes)", info.Size())
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	src := string(data)
	switch n := strings.Count(src, in.Old); {
	case n == 0:
		return "", fmt.Errorf("trecho não encontrado em %s", in.Path)
	case n > 1:
		return "", fmt.Errorf("trecho aparece %d vezes em %s; seja mais específico", n, in.Path)
	}
	out := strings.Replace(src, in.Old, in.New, 1)
	if err := os.WriteFile(full, []byte(out), info.Mode().Perm()); err != nil {
		return "", err
	}
	return fmt.Sprintf("arquivo editado: %s (%d → %d bytes)", in.Path, len(src), len(out)), nil
}

// DeleteFile remove um arquivo ou diretório dentro da raiz permitida. É uma
// tool mutante: exige aprovação humana.
type DeleteFile struct {
	Root string // raiz permitida (vazio = diretório atual)
}

func (DeleteFile) Name() string { return "delete_file" }
func (DeleteFile) Description() string {
	return "Remove um arquivo do projeto; diretórios exigem recursive=true (exige aprovação)."
}

func (DeleteFile) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string", "description": "caminho relativo à raiz do projeto"},
			"recursive": map[string]any{"type": "boolean", "description": "remove diretório e todo o seu conteúdo"},
		},
		"required": []string{"path"},
	}
}

func (DeleteFile) RequiresApproval() bool { return true }

func (t DeleteFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", errors.New(`argumento "path" é obrigatório`)
	}
	full, err := resolveWithin(t.Root, in.Path)
	if err != nil {
		return "", err
	}
	absRoot, err := resolveRoot(t.Root)
	if err != nil {
		return "", err
	}
	if full == absRoot {
		return "", errors.New("recusando remover a raiz do projeto")
	}
	info, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if info.IsDir() && !in.Recursive {
		return "", fmt.Errorf("caminho é um diretório; use recursive=true para removê-lo com o conteúdo: %s", in.Path)
	}
	if in.Recursive {
		if err := os.RemoveAll(full); err != nil {
			return "", err
		}
	} else if err := os.Remove(full); err != nil {
		return "", err
	}
	if info.IsDir() {
		return fmt.Sprintf("removido: %s (diretório e conteúdo)", in.Path), nil
	}
	return fmt.Sprintf("removido: %s (%d bytes)", in.Path, info.Size()), nil
}

// MoveFile move ou renomeia um arquivo/diretório dentro da raiz permitida.
// Exige aprovação.
type MoveFile struct {
	Root string // raiz permitida (vazio = diretório atual)
}

func (MoveFile) Name() string { return "move_file" }
func (MoveFile) Description() string {
	return "Move ou renomeia um arquivo/diretório dentro do projeto (exige aprovação)."
}

func (MoveFile) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string", "description": "origem, relativa à raiz do projeto"},
			"to":        map[string]any{"type": "string", "description": "destino, relativo à raiz do projeto"},
			"overwrite": map[string]any{"type": "boolean", "description": "permite substituir o destino se ele já existir"},
		},
		"required": []string{"path", "to"},
	}
}

func (MoveFile) RequiresApproval() bool { return true }

func (t MoveFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path      string `json:"path"`
		To        string `json:"to"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" || strings.TrimSpace(in.To) == "" {
		return "", errors.New(`argumentos "path" e "to" são obrigatórios`)
	}
	absRoot, err := resolveRoot(t.Root)
	if err != nil {
		return "", err
	}
	src, err := resolveWithin(t.Root, in.Path)
	if err != nil {
		return "", err
	}
	dst, err := resolveWithin(t.Root, in.To)
	if err != nil {
		return "", err
	}
	if src == absRoot {
		return "", errors.New("recusando mover a raiz do projeto")
	}
	if dst == absRoot {
		return "", errors.New("destino não pode ser a raiz do projeto")
	}
	info, err := os.Lstat(src)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if rel, relErr := filepath.Rel(src, dst); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("destino está dentro da origem: %s", in.To)
		}
	}
	if dstInfo, statErr := os.Lstat(dst); statErr == nil {
		if !in.Overwrite {
			return "", fmt.Errorf("destino já existe (use overwrite=true para substituir): %s", in.To)
		}
		if dstInfo.IsDir() {
			return "", fmt.Errorf("destino é um diretório: %s", in.To)
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if dir := filepath.Dir(dst); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return fmt.Sprintf("movido: %s → %s", in.Path, in.To), nil
}

// Mkdir cria um diretório (com os pais) dentro da raiz permitida. Exige
// aprovação.
type Mkdir struct {
	Root string // raiz permitida (vazio = diretório atual)
}

func (Mkdir) Name() string { return "mkdir" }
func (Mkdir) Description() string {
	return "Cria um diretório (com os pais) dentro do projeto (exige aprovação)."
}

func (Mkdir) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "diretório relativo à raiz do projeto"},
		},
		"required": []string{"path"},
	}
}

func (Mkdir) RequiresApproval() bool { return true }

func (t Mkdir) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", errors.New(`argumento "path" é obrigatório`)
	}
	full, err := resolveWithin(t.Root, in.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(full, 0o755); err != nil {
		return "", err
	}
	return fmt.Sprintf("diretório criado: %s", in.Path), nil
}

// shellMeta são metacaracteres recusados em run_shell: evitam encadear
// comandos e burlar a allowlist de prefixos. Pipes e redirecionamentos ficam de
// fora de propósito.
const shellMeta = "$`;&|><()\n\r"

// RunShell executa um comando simples (sem pipes/redirects) cujo prefixo esteja
// na allowlist, sempre com cwd dentro da raiz do projeto. Exige aprovação.
type RunShell struct {
	Root     string   // raiz permitida (vazio = diretório atual)
	Allow    []string // prefixos de comando liberados (ex.: "ls", "git status")
	Timeout  time.Duration
	MaxBytes int // limite de saída capturada (0 = 64 KiB)
}

func (RunShell) Name() string { return "run_shell" }
func (RunShell) Description() string {
	return "Executa um comando de shell permitido dentro do projeto (exige aprovação; sem pipes ou redirecionamentos)."
}

func (RunShell) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "comando simples, ex.: \"go test ./...\""},
			"cwd":     map[string]any{"type": "string", "description": "diretório de trabalho relativo à raiz (vazio = raiz)"},
		},
		"required": []string{"command"},
	}
}

func (RunShell) RequiresApproval() bool { return true }

func (t RunShell) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	cmdStr := strings.TrimSpace(in.Command)
	if cmdStr == "" {
		return "", errors.New(`argumento "command" é obrigatório`)
	}
	if err := CheckCommand(cmdStr, t.Allow); err != nil {
		return "", err
	}
	dir, err := resolveWithin(t.Root, in.Cwd)
	if err != nil {
		return "", err
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	limit := t.MaxBytes
	if limit <= 0 {
		limit = 64 * 1024
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	buf := &limitedBuffer{limit: limit}
	cmd := exec.CommandContext(runCtx, "sh", "-c", cmdStr)
	cmd.Dir = dir
	cmd.Stdout = buf
	cmd.Stderr = buf
	err = cmd.Run()
	out := buf.String()
	if runCtx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("comando excedeu o timeout de %s", timeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, fmt.Errorf("comando saiu com código %d: %s", exitErr.ExitCode(), firstLine(out))
		}
		return out, err
	}
	if strings.TrimSpace(out) == "" {
		return "(sem saída)", nil
	}
	return out, nil
}

// CheckCommand valida um comando contra a allowlist de prefixos e recusa
// metacaracteres de shell.
func CheckCommand(cmd string, allow []string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return errors.New("comando vazio")
	}
	if i := strings.IndexAny(cmd, shellMeta); i >= 0 {
		return fmt.Errorf("comando contém metacaractere não suportado %q; use um comando simples", string(cmd[i]))
	}
	cmd = strings.Join(strings.Fields(cmd), " ")
	if len(allow) == 0 {
		return errors.New("nenhum comando liberado; configure -shellallow")
	}
	for _, p := range allow {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if cmd == p || strings.HasPrefix(cmd, p+" ") {
			return nil
		}
	}
	return fmt.Errorf("comando não permitido: %q (prefixos liberados: %s)", cmd, strings.Join(allow, ", "))
}

// limitedBuffer acumula até limit bytes e sinaliza truncamento.
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		// O corte é por byte: recua até a fronteira de runa para não gravar
		// UTF-8 inválido no meio de um caractere acentuado.
		b.buf.WriteString(textutil.CutBytesUTF8(string(p), room))
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		s += fmt.Sprintf("\n[... saída truncada em %d bytes]", b.limit)
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.TrimSpace(s), 160)
}
