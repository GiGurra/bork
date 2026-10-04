package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/GiGurra/bork/internal/diag"
)

const (
	cacheArtifactSchema   = 1
	cacheArtifactLayout   = "complete-graph-v1"
	cacheArtifactMaxBytes = 64 << 20
	cacheArtifactMaxNodes = 1 << 20
	cacheArtifactMaxDepth = 32
)

var errInvalidCacheArtifact = errors.New("invalid compiler cache artifact")
var errCacheArtifactBudget = errors.New("compiler cache artifact exceeds encoding budget")

// Artifacts contain only owned results and receipts. No AST, checker pointers,
// successful evaluations or asset identities are represented by this schema.
type cacheArtifactBody struct {
	Schema      int                    `json:"schema"`
	Namespace   [sha256.Size]byte      `json:"namespace"`
	Key         [sha256.Size]byte      `json:"key"`
	Request     cacheArtifactRequest   `json:"request"`
	Source      *sourceReceipt         `json:"source"`
	Go          *goContextReceipt      `json:"go"`
	Names       []*goNameReceipt       `json:"names"`
	Module      cacheArtifactModule    `json:"module"`
	GoSource    []byte                 `json:"go_source"`
	SourcePaths []string               `json:"source_paths"`
	Warnings    []cacheArtifactWarning `json:"warnings"`
}
type cacheArtifactRequest struct {
	Path string `json:"path"`
	Cwd  string `json:"cwd"`
	Emit bool   `json:"emit"`
}
type cacheArtifactModule struct {
	Mod []byte `json:"mod"`
	Sum []byte `json:"sum"`
}

// Diagnostic.MarshalJSON deliberately normalizes positions/code. Cache encoding
// instead preserves their exact values, including empty End or Code fields.
type cacheArtifactWarning struct {
	Pos      diag.Pos   `json:"pos"`
	End      diag.Pos   `json:"end"`
	Message  string     `json:"message"`
	Code     string     `json:"code"`
	Severity string     `json:"severity"`
	Fixes    []diag.Fix `json:"fixes"`
}
type cacheArtifactEnvelope struct {
	Schema   int             `json:"schema"`
	Body     json.RawMessage `json:"body"`
	Checksum string          `json:"checksum"`
}

func cacheArtifactFrom(artifact *sessionArtifact, paths []string, namespace [sha256.Size]byte) (*cacheArtifactBody, error) {
	if artifact == nil || artifact.assets == nil || artifact.module == nil || artifact.inputs == nil || artifact.context == nil {
		return nil, errInvalidCacheArtifact
	}
	artifact.assets.mu.Lock()
	hasAssets := len(artifact.assets.reads) != 0
	artifact.assets.mu.Unlock()
	if hasAssets {
		return nil, errInvalidCacheArtifact
	}
	source, err := artifact.inputs.receipt()
	if err != nil {
		return nil, err
	}
	configuration, err := artifact.context.receipt()
	if err != nil {
		return nil, err
	}
	request := cacheArtifactRequest{Path: artifact.path, Cwd: source.Cwd, Emit: artifact.emit}
	key, err := request.key()
	if err != nil {
		return nil, err
	}
	body := &cacheArtifactBody{Schema: cacheArtifactSchema, Namespace: namespace, Key: key, Request: request, Source: source, Go: configuration, Module: cacheArtifactModule{Mod: slices.Clone(artifact.module.mod), Sum: slices.Clone(artifact.module.sum)}, GoSource: slices.Clone(artifact.goSrc), SourcePaths: slices.Clone(paths)}
	for _, input := range artifact.names {
		name, err := input.receipt()
		if err != nil {
			return nil, err
		}
		body.Names = append(body.Names, name)
	}
	warnings := cloneSessionDiagnostics(artifact.warnings)
	for _, warning := range warnings {
		body.Warnings = append(body.Warnings, cacheArtifactWarning{Pos: warning.Pos, End: warning.End, Message: warning.Msg, Code: warning.Code, Severity: warning.Severity, Fixes: warning.Fixes})
	}
	if !body.valid() || !artifact.inputs.current() || !artifact.assets.current() || !artifact.context.validation.current() {
		return nil, errInvalidCacheArtifact
	}
	return body, nil
}

func (r cacheArtifactRequest) key() ([sha256.Size]byte, error) {
	if r.Path == "" || !validReceiptPath(r.Path) || !validReceiptPath(r.Cwd) || !filepath.IsAbs(r.Cwd) {
		return [sha256.Size]byte{}, errInvalidCacheArtifact
	}
	data, err := json.Marshal(r)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func (b *cacheArtifactBody) valid() bool {
	if b == nil || b.Schema != cacheArtifactSchema || b.Source == nil || b.Go == nil {
		return false
	}
	key, err := b.Request.key()
	if err != nil || key != b.Key || b.Request.Cwd != b.Source.Cwd || b.Go.Inputs == nil || b.Go.Inputs.Cwd != b.Request.Cwd || !b.Go.valid() {
		return false
	}
	if _, err := b.Source.snapshot(); err != nil {
		return false
	}
	if _, err := b.Go.Inputs.snapshot(); err != nil {
		return false
	}
	for _, name := range b.Names {
		if name == nil || name.Directories == nil || name.Directories.Cwd != b.Request.Cwd {
			return false
		}
		if _, err := name.restore(&goContextValidation{root: b.Go.Root}); err != nil {
			return false
		}
	}
	if len(b.Module.Mod) == 0 || len(b.SourcePaths) == 0 || b.Request.Emit && len(b.GoSource) == 0 || !b.Request.Emit && len(b.GoSource) != 0 {
		return false
	}
	for _, path := range b.SourcePaths {
		if !validReceiptPath(path) {
			return false
		}
	}
	for _, warning := range b.Warnings {
		if warning.Severity != "warning" || !validReceiptPath(warning.Pos.File) || !validReceiptPath(warning.End.File) {
			return false
		}
		for _, fix := range warning.Fixes {
			for _, edit := range fix.Edits {
				if !validReceiptPath(edit.Start.File) || !validReceiptPath(edit.End.File) {
					return false
				}
			}
		}
	}
	return true
}

func encodeCacheArtifact(body *cacheArtifactBody) ([]byte, error) {
	// Bound before Marshal allocates a second copy of large compiler-owned data.
	budget := cacheEncodingBudget{remaining: cacheArtifactMaxBytes - 1024}
	if err := budget.value(reflect.ValueOf(body), 0); err != nil {
		return nil, err
	}
	if !body.valid() {
		return nil, errInvalidCacheArtifact
	}
	canonical, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	checksum := sha256.Sum256(canonical)
	envelope := cacheArtifactEnvelope{Schema: cacheArtifactSchema, Body: canonical, Checksum: hex.EncodeToString(checksum[:])}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if len(encoded) > cacheArtifactMaxBytes {
		return nil, errCacheArtifactBudget
	}
	return encoded, nil
}

func decodeCacheArtifact(reader io.Reader, namespace, key [sha256.Size]byte) (*cacheArtifactBody, error) {
	encoded, err := io.ReadAll(io.LimitReader(reader, cacheArtifactMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(encoded) > cacheArtifactMaxBytes {
		return nil, errCacheArtifactBudget
	}
	if err := validateCacheJSON(encoded); err != nil {
		return nil, err
	}
	var envelope cacheArtifactEnvelope
	if err := decodeStrictCacheJSON(encoded, &envelope); err != nil || envelope.Schema != cacheArtifactSchema {
		return nil, errInvalidCacheArtifact
	}
	checksum := sha256.Sum256(envelope.Body)
	if hex.EncodeToString(checksum[:]) != envelope.Checksum {
		return nil, errInvalidCacheArtifact
	}
	var body cacheArtifactBody
	if err := decodeStrictCacheJSON(envelope.Body, &body); err != nil {
		return nil, errInvalidCacheArtifact
	}
	// Require the canonical encoding. This rejects omitted fields, lossy strings,
	// overlong fixed arrays, and noncanonical representations before serving.
	canonical, err := json.Marshal(&body)
	if err != nil || !bytes.Equal(canonical, envelope.Body) || body.Namespace != namespace || body.Key != key || !body.valid() {
		return nil, errInvalidCacheArtifact
	}
	return &body, nil
}

func decodeStrictCacheJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errInvalidCacheArtifact
	}
	return nil
}

// Preflight duplicate keys and structural budgets before typed materialization.
func validateCacheJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	var value func(int) error
	value = func(depth int) error {
		nodes++
		if nodes > cacheArtifactMaxNodes || depth > cacheArtifactMaxDepth {
			return errCacheArtifactBudget
		}
		token, err := decoder.Token()
		if err != nil {
			return errInvalidCacheArtifact
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return errInvalidCacheArtifact
				}
				key, ok := token.(string)
				if !ok || seen[key] {
					return errInvalidCacheArtifact
				}
				seen[key] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errInvalidCacheArtifact
			}
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errInvalidCacheArtifact
			}
		default:
			return errInvalidCacheArtifact
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errInvalidCacheArtifact
	}
	return nil
}

// Conservative JSON size bound for this data-only schema. In particular, byte
// slices use base64 rather than per-byte node/string overhead. Unknown future
// data kinds must introduce an explicit bound before becoming persistable.
type cacheEncodingBudget struct{ remaining, nodes int }

func (b *cacheEncodingBudget) take(bytes int) error {
	if bytes < 0 || bytes > b.remaining {
		return errCacheArtifactBudget
	}
	b.remaining -= bytes
	return nil
}
func (b *cacheEncodingBudget) value(v reflect.Value, depth int) error {
	b.nodes++
	if b.nodes > cacheArtifactMaxNodes || depth > cacheArtifactMaxDepth {
		return errCacheArtifactBudget
	}
	if !v.IsValid() {
		return b.take(4)
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return b.take(4)
		}
		return b.value(v.Elem(), depth+1)
	case reflect.String:
		text := v.String()
		if !utf8.ValidString(text) {
			return errInvalidCacheArtifact
		}
		if len(text) > b.remaining/6 {
			return errCacheArtifactBudget
		}
		return b.take(6*len(text) + 2)
	case reflect.Bool:
		return b.take(5)
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint32, reflect.Uint64:
		return b.take(20)
	case reflect.Slice:
		if v.IsNil() {
			return b.take(4)
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			if v.Len() > b.remaining {
				return errCacheArtifactBudget
			}
			return b.take(4*((v.Len()+2)/3) + 2)
		}
		fallthrough
	case reflect.Array:
		if err := b.take(v.Len() + 2); err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			if err := b.value(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return b.take(4)
		}
		if v.Type().Key().Kind() != reflect.String {
			return errInvalidCacheArtifact
		}
		if err := b.take(2*v.Len() + 2); err != nil {
			return err
		}
		entries := v.MapRange()
		for entries.Next() {
			if err := b.value(entries.Key(), depth+1); err != nil {
				return err
			}
			if err := b.value(entries.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		if err := b.take(2*v.NumField() + 2); err != nil {
			return err
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "" {
				name = field.Name
			}
			if name == "-" {
				continue
			}
			if err := b.take(6*len(name) + 2); err != nil {
				return err
			}
			if err := b.value(v.Field(i), depth+1); err != nil {
				return err
			}
		}
	default:
		return errInvalidCacheArtifact
	}
	return nil
}
