package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// ComptimeValuePolicyVersion versions the transport limits independently of
// the value schema. Future cache keys must include both versions.
const (
	ComptimeValuePolicyVersion = 1
	ComptimeResultLimit        = 16 << 20
	ComptimeNodeLimit          = 1000000
	ComptimeDepthLimit         = 256
)

type comptimeTransportReader struct {
	decoder  *json.Decoder
	nodes    int
	maxDepth int
}

// Parse only the compiler-owned schema. Budget each node before parsing or
// retaining its fields, so malformed evaluator/cache data cannot materialize
// an over-budget tree before validation. JSON strings remain byte-size bounded
// by DecodeComptime's result limit.
func parseComptimeTransport(data []byte, nodes, maxDepth int) (comptimeValue, error) {
	r := comptimeTransportReader{decoder: json.NewDecoder(bytes.NewReader(data)), nodes: nodes, maxDepth: maxDepth}
	r.decoder.UseNumber()
	var value comptimeValue
	if err := r.delimiter('{'); err != nil {
		return value, err
	}
	seen := map[string]bool{}
	for r.decoder.More() {
		field, err := r.field(seen)
		if err != nil {
			return value, err
		}
		switch field {
		case "Version":
			token, err := r.decoder.Token()
			if err != nil {
				return value, err
			}
			number, ok := token.(json.Number)
			if !ok {
				return value, fmt.Errorf("invalid result schema version")
			}
			version, err := strconv.Atoi(string(number))
			if err != nil || version != ComptimeSchemaVersion {
				return value, fmt.Errorf("unsupported result schema %s", number)
			}
		case "Value":
			value, err = r.value(0)
			if err != nil {
				return value, err
			}
		default:
			return value, fmt.Errorf("unknown result field %q", field)
		}
	}
	if err := r.delimiter('}'); err != nil {
		return value, err
	}
	if !seen["Version"] || !seen["Value"] {
		return value, fmt.Errorf("missing result Version or Value")
	}
	if _, err := r.decoder.Token(); err != io.EOF {
		return value, fmt.Errorf("unexpected trailing result data")
	}
	return value, nil
}

func (r *comptimeTransportReader) value(depth int) (comptimeValue, error) {
	var value comptimeValue
	if depth > r.maxDepth || r.nodes <= 0 {
		return value, fmt.Errorf("result exceeds depth or node limit")
	}
	r.nodes--
	if err := r.delimiter('{'); err != nil {
		return value, err
	}
	seen := map[string]bool{}
	for r.decoder.More() {
		field, err := r.field(seen)
		if err != nil {
			return value, err
		}
		switch field {
		case "Kind", "Text", "Tag":
			token, err := r.decoder.Token()
			if err != nil {
				return value, err
			}
			text, ok := token.(string)
			if !ok {
				return value, fmt.Errorf("result %s must be a string", field)
			}
			switch field {
			case "Kind":
				value.Kind = text
			case "Text":
				value.Text = text
			case "Tag":
				value.Tag = text
			}
		case "Nil":
			token, err := r.decoder.Token()
			if err != nil {
				return value, err
			}
			var ok bool
			value.Nil, ok = token.(bool)
			if !ok {
				return value, fmt.Errorf("result Nil must be a boolean")
			}
		case "Items":
			token, err := r.decoder.Token()
			if err != nil {
				return value, err
			}
			if token == nil {
				continue // Generated zero-field values use null Items.
			}
			if token != json.Delim('[') {
				return value, fmt.Errorf("result Items must be an array or null")
			}
			for r.decoder.More() {
				item, err := r.value(depth + 1)
				if err != nil {
					return value, err
				}
				value.Items = append(value.Items, item)
			}
			if err := r.delimiter(']'); err != nil {
				return value, err
			}
		default:
			return value, fmt.Errorf("unknown result value field %q", field)
		}
	}
	if err := r.delimiter('}'); err != nil {
		return value, err
	}
	return value, nil
}

func (r *comptimeTransportReader) field(seen map[string]bool) (string, error) {
	token, err := r.decoder.Token()
	if err != nil {
		return "", err
	}
	field, ok := token.(string)
	if !ok || seen[field] {
		return "", fmt.Errorf("invalid or duplicate result field %q", field)
	}
	seen[field] = true
	return field, nil
}

func (r *comptimeTransportReader) delimiter(want json.Delim) error {
	token, err := r.decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("expected result delimiter %q", want)
	}
	return nil
}
