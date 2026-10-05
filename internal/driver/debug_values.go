package driver

import (
	"github.com/GiGurra/bork/internal/gen"
	"strconv"
	"strings"
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
					pos = skipSpace(text, close)
				}
			}
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
				if _, err := strconv.ParseFloat(value, 64); err == nil && !strings.ContainsAny(value, ".eE") {
					value += ".0"
				}
			}
			parts[i] = field + ": " + value
		} else {
			parts[i] = r.pretty(strings.TrimSpace(part))
		}
	}
	return strings.Join(parts, ", ")
}

func (r *dapRelay) prettyType(text string) string {
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
