// Package textutil concentra o corte de texto com limite de runas e de bytes.
// O corte por bytes nunca parte uma sequência UTF-8 ao meio: recua até o início
// da runa (ou seja, até o último byte que não é de continuação). É a base das
// mensagens, logs, buffers e do store — todos lidam com texto do usuário e do
// modelo, frequentemente em português (acentos ocupam mais de um byte).
package textutil

import "unicode/utf8"

// Ellipsis marca visualmente que o texto foi cortado.
const Ellipsis = "…"

// TruncateRunes devolve s com no máximo limit runas. Se cortar, acrescenta
// Ellipsis.
func TruncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + Ellipsis
}

// CutBytesUTF8 devolve o maior prefixo de s com no máximo maxBytes bytes que
// termina em uma fronteira de runa (não parte UTF-8). Não acrescenta sufixo, o
// que é obrigatório em contextos que o chamador mede em bytes (arquivos,
// buffers de saída).
func CutBytesUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// TruncateBytesUTF8 é CutBytesUTF8 com o marcador de corte: devolve o maior
// prefixo de s com no máximo maxBytes bytes, terminando em fronteira de runa, e
// acrescenta Ellipsis quando houve corte.
func TruncateBytesUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	return CutBytesUTF8(s, maxBytes) + Ellipsis
}
