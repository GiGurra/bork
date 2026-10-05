package driver

import (
	"math"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/gen"
)

// Delve summaries are bounded previews. Transform only complete compiler-known
// values, preserving quoted strings, truncation markers and unloaded payloads.
func (r *dapRelay) pretty(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] == '"' || text[i] == '\'' || text[i] == '`' {
			end := quotedEnd(text, i)
			out.WriteString(text[i:end])
			i = end
			continue
		}
		if !typeChar(text[i]) {
			out.WriteByte(text[i])
			i++
			continue
		}
		start := i
		for i < len(text) && typeChar(text[i]) {
			i++
		}
		end := i
		if end < len(text) && text[end] == '[' {
			if close := balancedEnd(text, end, '[', ']'); close > 0 {
				end = close
			}
		}
		token := text[start:end]
		typ, known := r.typeInfo(token)
		dynamicScalar := false
		pos := skipSpace(text, end)
		if !known && token == "interface" && pos+2 <= len(text) && text[pos:pos+2] == "{}" {
			pos = skipSpace(text, pos+2)
		}
		// Interfaces carry the dynamic variant in parentheses, followed by its
		// struct payload. Keep the dynamic value and omit the Go interface wrapper.
		if pos < len(text) && text[pos] == '(' {
			close := balancedEnd(text, pos, '(', ')')
			if close > 0 {
				dynamic := text[pos+1 : close-1]
				if dt, ok := r.typeInfo(dynamic); ok {
					typ, known = dt, true
					dynamicScalar = dt.Kind == "scalar" || dt.Kind == "container"
					pos = skipSpace(text, close)
				}
			}
		}
		if dynamicScalar {
			payload := r.pretty(text[pos:])
			if typ.Name == "Float" || typ.Name == "Float32" {
				payload = floatPreview(payload)
			}
			out.WriteString(payload)
			return out.String()
		}
		if known && pos < len(text) && text[pos] == '{' {
			close := balancedEnd(text, pos, '{', '}')
			if close > 0 {
				payload := r.prettyFields(text[pos+1:close-1], typ)
				switch {
				case typ.Kind == "option" && len(typ.Fields) == 0:
					out.WriteString(typ.Name)
				case typ.Kind == "option":
					if colon := strings.IndexByte(payload, ':'); colon >= 0 {
						out.WriteString(typ.Name + "(" + strings.TrimSpace(payload[colon+1:]) + ")")
					} else {
						out.WriteString(typ.Name + " { " + payload + " }")
					}
				case typ.Kind == "variant" && strings.TrimSpace(payload) == "":
					out.WriteString(typ.Name)
				default:
					out.WriteString(typ.Name + " { " + payload + " }")
				}
				i = close
				continue
			}
			out.WriteString(text[start:])
			return out.String()
		}
		if known {
			out.WriteString(r.prettyType(token))
			i = end
			continue
		}
		out.WriteString(text[start:i])
	}
	return out.String()
}

func (r *dapRelay) prettyFields(text string, typ gen.DebugType) string {
	parts := splitTop(text, ',')
	for i, part := range parts {
		pair := splitTop(part, ':')
		if len(pair) == 2 {
			field := strings.TrimSpace(pair[0])
			if label, ok := typ.Fields[field]; ok {
				field = label.Name
			}
			value := r.pretty(strings.TrimSpace(pair[1]))
			if f, ok := typ.Fields[strings.TrimSpace(pair[0])]; ok && (f.Type == "Float" || f.Type == "Float32") {
				value = floatPreview(value)
			}
			parts[i] = field + ": " + value
		} else {
			parts[i] = r.pretty(strings.TrimSpace(part))
		}
	}
	return strings.Join(parts, ", ")
}

func (r *dapRelay) prettyType(text string) string {
	if t, ok := r.metadata.Types[strings.ReplaceAll(text, " ", "")]; ok {
		return t.Name
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if !typeChar(text[i]) {
			out.WriteByte(text[i])
			i++
			continue
		}
		start := i
		for i < len(text) && typeChar(text[i]) {
			i++
		}
		word := text[start:i]
		if typ, ok := r.typeInfo(word); ok {
			out.WriteString(typ.Name)
		} else {
			out.WriteString(word)
		}
	}
	return out.String()
}

func typeChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '/'
}
func skipSpace(text string, i int) int {
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	return i
}
func quotedEnd(text string, i int) int {
	quote := text[i]
	i++
	for i < len(text) {
		if text[i] == quote {
			return i + 1
		}
		if text[i] == '\\' && quote != '`' {
			i++
		}
		i++
	}
	return len(text)
}
func balancedEnd(text string, start int, open, close byte) int {
	depth := 0
	for i := start; i < len(text); i++ {
		if text[i] == '"' || text[i] == '\'' || text[i] == '`' {
			i = quotedEnd(text, i) - 1
			continue
		}
		if text[i] == open {
			depth++
		}
		if text[i] == close {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
func splitTop(text string, separator byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"', '\'', '`':
			i = quotedEnd(text, i) - 1
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			depth--
		default:
			if text[i] == separator && depth == 0 {
				parts = append(parts, text[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, text[start:])
}

func floatPreview(text string) string {
	if n, err := strconv.ParseFloat(text, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && !strings.ContainsAny(text, ".eE") {
		return text + ".0"
	}
	return text
}

// The same preview prefix is available when clients do not request DAP type
// fields. Delve always includes generated record/interface types in summaries.
func previewType(text string) string {
	i := 0
	for i+2 <= len(text) && text[i:i+2] == "[]" {
		i += 2
	}
	for i < len(text) && typeChar(text[i]) {
		i++
	}
	if i == 0 {
		return ""
	}
	if i < len(text) && text[i] == '[' {
		end := balancedEnd(text, i, '[', ']')
		if end < 0 {
			return ""
		}
		i = end
	}
	next := skipSpace(text, i)
	if text[:i] == "interface" && next+2 <= len(text) && text[next:next+2] == "{}" {
		i = next + 2
		next = skipSpace(text, i)
	}
	if next < len(text) && text[next] == '(' {
		if end := balancedEnd(text, next, '(', ')'); end > 0 {
			i = end
		}
	}
	return text[:i]
}
