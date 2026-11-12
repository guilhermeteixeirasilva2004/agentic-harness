package llm

// Role identifica o autor de uma mensagem.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message é uma mensagem no formato de chat da API compatível com OpenAI.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatRequest é o corpo enviado para /chat/completions.
type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Stream         bool            `json:"stream"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat controla a forma da saída do modelo. O llama-server aceita
// "text", "json_object" e "json_schema" (este último converte o schema em
// gramática e garante JSON válido).
type ResponseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

// JSONSchema descreve o objeto que o modelo deve produzir.
type JSONSchema struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict,omitempty"`
}

// chatChunk é um pedaço de streaming (SSE) da resposta.
type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// ChatResponse é a resposta não-streaming de /chat/completions.
type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// EmbeddingRequest é o corpo enviado para /embeddings.
type EmbeddingRequest struct {
	Model string `json:"model,omitempty"`
	Input any    `json:"input"` // string ou []string
}

// EmbeddingResponse é a resposta de /embeddings.
type EmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}
