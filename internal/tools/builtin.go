package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GetTime devolve a data e a hora atuais no fuso local.
type GetTime struct{}

func (GetTime) Name() string        { return "get_time" }
func (GetTime) Description() string { return "Retorna a data e a hora atuais no fuso local." }

func (GetTime) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (GetTime) Run(_ context.Context, _ json.RawMessage) (string, error) {
	return time.Now().Format("2006-01-02 15:04:05 (Monday) -07:00"), nil
}

// ReadFile lê um arquivo de texto dentro de uma raiz permitida.
type ReadFile struct {
	Root     string // raiz permitida (vazio = diretório atual)
	MaxBytes int    // limite de leitura (0 = 8192)
}

func (ReadFile) Name() string { return "read_file" }
func (ReadFile) Description() string {
	return "Lê um arquivo de texto dentro do projeto (até o limite de bytes)."
}

func (ReadFile) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string", "description": "caminho relativo à raiz do projeto"},
			"max_bytes": map[string]any{"type": "integer", "description": "máximo de bytes a ler"},
		},
		"required": []string{"path"},
	}
}

func (t ReadFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
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

	limit := in.MaxBytes
	if limit <= 0 {
		limit = t.MaxBytes
	}
	if limit <= 0 {
		limit = 8192
	}
	if limit > 256*1024 {
		limit = 256 * 1024
	}

	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", err
	}
	out := string(data)
	if len(data) > limit {
		out = string(data[:limit]) + fmt.Sprintf("\n[... truncado em %d bytes]", limit)
	}
	return out, nil
}

// ListDir lista entradas de um diretório dentro da raiz permitida.
type ListDir struct {
	Root string // raiz permitida (vazio = diretório atual)
}

func (ListDir) Name() string { return "list_dir" }
func (ListDir) Description() string {
	return "Lista arquivos e subdiretórios de um diretório do projeto."
}

func (ListDir) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "diretório relativo à raiz (vazio = raiz)"},
		},
	}
}

func (t ListDir) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("argumentos inválidos: %w", err)
		}
	}
	full, err := resolveWithin(t.Root, in.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

// resolveRoot devolve o caminho absoluto da raiz permitida, já com symlinks
// resolvidos quando possível.
func resolveRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	return absRoot, nil
}

// resolveWithin resolve path sob root e recusa caminhos que escapem da raiz,
// inclusive por symlinks (o alvo real também precisa ficar dentro da raiz).
func resolveWithin(root, path string) (string, error) {
	absRoot, err := resolveRoot(root)
	if err != nil {
		return "", err
	}
	candidate := path
	if strings.TrimSpace(candidate) == "" {
		candidate = "."
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absRoot, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if err := ensureWithin(absRoot, abs, path); err != nil {
		return "", err
	}
	return abs, nil
}

// ensureWithin confere se abs (resolvendo symlinks existentes) fica sob absRoot.
func ensureWithin(absRoot, abs, display string) error {
	real := abs
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		real = resolved
	} else if parent, perr := filepath.EvalSymlinks(filepath.Dir(abs)); perr == nil {
		real = filepath.Join(parent, filepath.Base(abs))
	}
	rel, err := filepath.Rel(absRoot, real)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("caminho fora da raiz permitida: %s", display)
	}
	return nil
}
