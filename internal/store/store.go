// Package store persiste sessões do harness em um arquivo NDJSON append-only.
// Cada linha é um Record independente, então concatenar ou reabrir o arquivo
// nunca corrompe os registros anteriores. É puro (só stdlib), de modo que os
// testes possam usá-lo com diretórios temporários.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Record é uma linha do arquivo de sessões.
type Record struct {
	Type    string          `json:"type"`
	Session string          `json:"session"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Store é um escritor append-only de registros NDJSON.
type Store struct {
	path string
	mu   sync.Mutex
	file *os.File
}

// Open cria (se preciso) os diretórios pais e abre o arquivo para escrita.
func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("caminho do store vazio")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, file: f}, nil
}

// Path devolve o caminho do arquivo em uso.
func (s *Store) Path() string { return s.path }

// Close fecha o arquivo subjacente.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// Append grava um registro, serializando o payload como JSON.
func (s *Store) Append(kind, session string, payload any) error {
	rec := Record{Type: kind, Session: session, At: time.Now()}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		rec.Payload = b
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return fmt.Errorf("store fechado")
	}
	_, err = s.file.Write(append(line, '\n'))
	return err
}

// Query lê todos os registros do arquivo, ignorando linhas vazias ou
// corrompidas (uma escrita interrompida não invalida o restante).
func (s *Store) Query() ([]Record, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out []Record
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
