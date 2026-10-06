package check

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Plans contain serialized syntax and stable source anchors, never checker
// nodes, dictionaries, descriptor handles, or pointers into a checked program.
const derivePlanABI = "shape-plan-v1"
const derivePlanMaxBytes = 16 << 20
const derivePlanMaxNodes = 1 << 20
const derivePlanMaxDepth = 256

type derivePlanNode struct {
	Kind     string           `json:"kind"`
	Type     string           `json:"type,omitempty"`
	Value    string           `json:"value,omitempty"`
	Anchor   string           `json:"anchor,omitempty"`
	Nil      bool             `json:"nil,omitempty"`
	Children []derivePlanNode `json:"children,omitempty"`
}

type derivePlanDocument struct {
	ABI   string         `json:"abi"`
	Key   string         `json:"key"`
	Hash  string         `json:"hash,omitempty"`
	Work  int            `json:"work"`
	Depth int            `json:"depth"`
	Body  derivePlanNode `json:"body"`
}

type derivePlanCodec struct {
	positions map[string]diag.Pos
	anchors   map[diag.Pos][]deriveSourceAnchor
	types     map[string]reflect.Type
	nodes     int
}

type deriveSourceAnchor struct {
	path  string
	owner reflect.Type
	field string
}

func newDerivePlanCodec(source *syntax.Block) *derivePlanCodec {
	codec := &derivePlanCodec{positions: map[string]diag.Pos{"": {}}, anchors: map[diag.Pos][]deriveSourceAnchor{}, types: map[string]reflect.Type{}}
	seen := map[reflect.Value]bool{}
	var scan func(reflect.Value, string, reflect.Type, string)
	scan = func(value reflect.Value, path string, owner reflect.Type, field string) {
		if !value.IsValid() {
			return
		}
		codec.types[value.Type().String()] = value.Type()
		if value.Type() == reflect.TypeFor[diag.Pos]() {
			position := value.Interface().(diag.Pos)
			if position.File != "" {
				codec.positions[path] = position
				codec.anchors[position] = append(codec.anchors[position], deriveSourceAnchor{path: path, owner: owner, field: field})
			}
			return
		}
		switch value.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !value.IsNil() {
				if value.Kind() == reflect.Pointer {
					if seen[value] {
						return
					}
					seen[value] = true
				}
				scan(value.Elem(), path, owner, field)
			}
		case reflect.Struct:
			for i := range value.NumField() {
				if value.Type().Field(i).IsExported() {
					scan(value.Field(i), path+"/"+value.Type().Field(i).Name, value.Type(), value.Type().Field(i).Name)
				}
			}
		case reflect.Slice:
			for i := range value.Len() {
				scan(value.Index(i), path+"/"+strconv.Itoa(i), owner, field)
			}
		}
	}
	scan(reflect.ValueOf(source), "body", nil, "")
	// Metadata evaluation can introduce these scalar literal nodes even if
	// the source contains only descriptor projections.
	for _, value := range []any{&syntax.StringLit{}, &syntax.BoolLit{}, &syntax.IntLit{}, &syntax.Block{}, &syntax.ListLit{}} {
		scan(reflect.ValueOf(value), "", nil, "")
	}
	return codec
}

var errDerivePlan = errors.New("invalid derive expansion plan")

func (codec *derivePlanCodec) encode(value reflect.Value, depth int, owner reflect.Type, field string) (derivePlanNode, error) {
	codec.nodes++
	if codec.nodes > derivePlanMaxNodes || depth > derivePlanMaxDepth || !value.IsValid() {
		return derivePlanNode{}, errDerivePlan
	}
	if value.Type() == reflect.TypeFor[diag.Pos]() {
		position := value.Interface().(diag.Pos)
		if position.File == "" {
			return derivePlanNode{Kind: "position"}, nil
		}
		candidates := codec.anchors[position]
		if len(candidates) == 0 {
			return derivePlanNode{}, errDerivePlan
		}
		// Cloned nodes retain the exact syntax-field anchor. A scalar
		// literal introduced by metadata evaluation uses its source
		// expression's Pos field rather than a coincident token endpoint.
		for _, candidate := range candidates {
			if candidate.owner == owner && candidate.field == field {
				return derivePlanNode{Kind: "position", Anchor: candidate.path}, nil
			}
		}
		if field == "Pos" {
			for _, candidate := range candidates {
				if candidate.field == "Pos" {
					return derivePlanNode{Kind: "position", Anchor: candidate.path}, nil
				}
			}
		}
		return derivePlanNode{}, errDerivePlan
	}
	node := derivePlanNode{Kind: value.Kind().String()}
	child := func(value reflect.Value, owner reflect.Type, field string) error {
		encoded, err := codec.encode(value, depth+1, owner, field)
		if err == nil {
			node.Children = append(node.Children, encoded)
		}
		return err
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		node.Nil = value.IsNil()
		if !node.Nil {
			if value.Kind() == reflect.Interface {
				node.Type = value.Elem().Type().String()
			}
			if err := child(value.Elem(), owner, field); err != nil {
				return derivePlanNode{}, err
			}
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if !value.Type().Field(i).IsExported() {
				return derivePlanNode{}, errDerivePlan
			}
			if err := child(value.Field(i), value.Type(), value.Type().Field(i).Name); err != nil {
				return derivePlanNode{}, err
			}
		}
	case reflect.Slice:
		node.Nil = value.IsNil()
		for i := range value.Len() {
			if err := child(value.Index(i), owner, field); err != nil {
				return derivePlanNode{}, err
			}
		}
	case reflect.String:
		node.Value = value.String()
	case reflect.Bool:
		node.Value = strconv.FormatBool(value.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		node.Value = strconv.FormatInt(value.Int(), 10)
	default:
		return derivePlanNode{}, errDerivePlan
	}
	return node, nil
}

func (codec *derivePlanCodec) decode(node derivePlanNode, typ reflect.Type, depth int) (reflect.Value, error) {
	codec.nodes++
	if codec.nodes > derivePlanMaxNodes || depth > derivePlanMaxDepth {
		return reflect.Value{}, errDerivePlan
	}
	if typ == reflect.TypeFor[diag.Pos]() {
		position, present := codec.positions[node.Anchor]
		if node.Kind != "position" || !present || len(node.Children) != 0 {
			return reflect.Value{}, errDerivePlan
		}
		return reflect.ValueOf(position), nil
	}
	if node.Kind != typ.Kind().String() {
		return reflect.Value{}, errDerivePlan
	}
	value := reflect.New(typ).Elem()
	one := func(childType reflect.Type) (reflect.Value, error) {
		if len(node.Children) != 1 {
			return reflect.Value{}, errDerivePlan
		}
		return codec.decode(node.Children[0], childType, depth+1)
	}
	if node.Nil {
		if len(node.Children) != 0 || typ.Kind() != reflect.Pointer && typ.Kind() != reflect.Interface && typ.Kind() != reflect.Slice {
			return reflect.Value{}, errDerivePlan
		}
		return value, nil
	}
	switch typ.Kind() {
	case reflect.Pointer:
		child, err := one(typ.Elem())
		if err != nil {
			return reflect.Value{}, err
		}
		value.Set(reflect.New(typ.Elem()))
		value.Elem().Set(child)
	case reflect.Interface:
		actual := codec.types[node.Type]
		if actual == nil || !actual.Implements(typ) {
			return reflect.Value{}, errDerivePlan
		}
		child, err := one(actual)
		if err != nil {
			return reflect.Value{}, err
		}
		value.Set(child)
	case reflect.Struct:
		if len(node.Children) != typ.NumField() {
			return reflect.Value{}, errDerivePlan
		}
		for i := range typ.NumField() {
			if !typ.Field(i).IsExported() {
				return reflect.Value{}, errDerivePlan
			}
			child, err := codec.decode(node.Children[i], typ.Field(i).Type, depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			value.Field(i).Set(child)
		}
	case reflect.Slice:
		if len(node.Children) > derivePlanMaxNodes-codec.nodes {
			return reflect.Value{}, errDerivePlan
		}
		value.Set(reflect.MakeSlice(typ, len(node.Children), len(node.Children)))
		for i, encoded := range node.Children {
			child, err := codec.decode(encoded, typ.Elem(), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			value.Index(i).Set(child)
		}
	case reflect.String:
		if len(node.Children) != 0 {
			return reflect.Value{}, errDerivePlan
		}
		value.SetString(node.Value)
	case reflect.Bool:
		if len(node.Children) != 0 {
			return reflect.Value{}, errDerivePlan
		}
		boolean, err := strconv.ParseBool(node.Value)
		if err != nil {
			return reflect.Value{}, errDerivePlan
		}
		value.SetBool(boolean)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if len(node.Children) != 0 {
			return reflect.Value{}, errDerivePlan
		}
		integer, err := strconv.ParseInt(node.Value, 10, typ.Bits())
		if err != nil {
			return reflect.Value{}, errDerivePlan
		}
		value.SetInt(integer)
	default:
		return reflect.Value{}, errDerivePlan
	}
	return value, nil
}

func encodeDerivePlan(source, body *syntax.Block, key string, work, depth int) []byte {
	codec := newDerivePlanCodec(source)
	node, err := codec.encode(reflect.ValueOf(body), 0, nil, "")
	if err != nil {
		return nil
	}
	document := derivePlanDocument{ABI: derivePlanABI, Key: key, Work: work, Depth: depth, Body: node}
	unsigned, err := json.Marshal(document)
	if err != nil || len(unsigned) > derivePlanMaxBytes {
		return nil
	}
	digest := sha256.Sum256(unsigned)
	document.Hash = hex.EncodeToString(digest[:])
	data, err := json.Marshal(document)
	if err != nil || len(data) > derivePlanMaxBytes {
		return nil
	}
	return data
}

func decodeDerivePlan(source *syntax.Block, key string, data []byte) (*syntax.Block, int, int) {
	if len(data) == 0 || len(data) > derivePlanMaxBytes {
		return nil, 0, 0
	}
	// Validate JSON nesting before allocating the plan's recursive tree.
	decoder := json.NewDecoder(bytes.NewReader(data))
	nesting, tokens := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, 0
		}
		tokens++
		if tokens > derivePlanMaxNodes*12 {
			return nil, 0, 0
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter == '{' || delimiter == '[' {
				nesting++
				if nesting > derivePlanMaxDepth*3 {
					return nil, 0, 0
				}
			} else {
				nesting--
			}
		}
	}
	var document derivePlanDocument
	if json.Unmarshal(data, &document) != nil || document.ABI != derivePlanABI || document.Key != key || document.Work < 1 || document.Work >= 100000 || document.Depth < 1 || document.Depth > 256 {
		return nil, 0, 0
	}
	expected := document.Hash
	document.Hash = ""
	unsigned, err := json.Marshal(document)
	if err != nil {
		return nil, 0, 0
	}
	digest := sha256.Sum256(unsigned)
	if expected != hex.EncodeToString(digest[:]) {
		return nil, 0, 0
	}
	codec := newDerivePlanCodec(source)
	body, err := codec.decode(document.Body, reflect.TypeFor[*syntax.Block](), 0)
	if err != nil || body.IsNil() {
		return nil, 0, 0
	}
	return body.Interface().(*syntax.Block), document.Work, document.Depth
}
