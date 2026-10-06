package syntax

import (
	"github.com/GiGurra/bork/internal/diag"
	"testing"
)

func TestParseTagGroups(t *testing.T) {
	source := `import codec "bork/codec"
type Row = {
  name: String = "Ada" codec { name: Option.Some("login"), aliases: ["user", "uid"] } go { json: "login" }
  count: Int codec { omit: codec.Omit.Default }
} codec { naming: codec.Naming.Snake } derive (codec.Decode)
type Mode = sealed {
  Plain codec { name: "plain" }
  Named { value: Int codec { name: "v" } } codec { aliases: ["old"] } where ok
  Positional(String) codec { fallback: true }
  codec { value: String }
} codec { naming: codec.Naming.Verbatim }
`
	d := &diag.List{}
	f := Parse("tags.bork", []byte(source), d)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	row := f.Types[0]
	if len(row.TagGroups) != 1 || row.TagGroups[0].Name != "codec" || len(row.Derive) != 1 {
		t.Fatalf("type groups: %+v", row)
	}
	field := row.Fields[0]
	if len(field.TagGroups) != 2 || field.TagGroups[1].Name != "go" || len(field.TagGroups[0].Entries) != 2 {
		t.Fatalf("field groups: %+v", field)
	}
	if _, ok := field.Default.(*StringLit); !ok {
		t.Fatalf("default swallowed group: %#v", field.Default)
	}
	if _, ok := field.TagGroups[0].Entries[1].Value.(*ListLit); !ok {
		t.Fatalf("alias expression: %#v", field.TagGroups[0].Entries[1].Value)
	}
	tags := field.GoTags()
	if len(tags) != 1 || tags[0].Name != "json" || tags[0].Value != "login" {
		t.Fatalf("go compatibility: %+v", tags)
	}
	mode := f.Types[1]
	if len(mode.Variants) != 4 || mode.Variants[3].Name != "codec" || len(mode.Variants[3].TagGroups) != 0 {
		t.Fatalf("newline variant parsed as group: %+v", mode.Variants)
	}
	for _, variant := range mode.Variants[:3] {
		if len(variant.TagGroups) != 1 {
			t.Fatalf("variant %s groups: %+v", variant.Name, variant.TagGroups)
		}
	}
	if len(mode.Variants[1].Where) != 1 || !mode.Variants[2].Positional {
		t.Fatal("variant payload/where lost")
	}
}

func TestTagGroupSameLine(t *testing.T) {
	for _, source := range []string{
		"type Row = { value: Int\n codec { name: \"v\" } }",
		"type Row = { value: Int }\n codec { name: \"v\" }",
		"type Row = { value: Int /*\n*/ codec { name: \"v\" } }",
	} {
		d := &diag.List{}
		Parse("tags.bork", []byte(source), d)
		if d.Len() == 0 {
			t.Fatalf("accepted detached tag group: %s", source)
		}
	}
}

func TestGoTagValuesRemainStrings(t *testing.T) {
	d := &diag.List{}
	Parse("tags.bork", []byte("type Row = { value: Int go { json: true } }"), d)
	if d.Len() == 0 {
		t.Fatal("accepted non-string Go tag")
	}
}

func TestTagGroupComments(t *testing.T) {
	d := &diag.List{}
	f := Parse("tags.bork", []byte("type Row = { value: Int codec /* inline */ { name: \"v\" } }\ntype Mode = sealed { Plain /*\n*/ codec { value: String } }\n"), d)
	if d.Len() != 0 {
		t.Fatal(d.Error())
	}
	if len(f.Types[0].Fields[0].TagGroups) != 1 || len(f.Types[1].Variants) != 2 {
		t.Fatal("comment changed group boundaries")
	}
}
