package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Tag bodies are ordinary checked record literals in the declaring package.
// Retain the checked literal, including inserted defaults, for shape queries.
func (c *checker) checkTagGroups(files []*syntax.File) {
	c.info.tagGroups = map[*syntax.TagGroup]*syntax.RecordLit{}
	for _, file := range files {
		c.inFile(file)
		for _, decl := range file.Types {
			supported := decl.Kind == syntax.RecordType || decl.Kind == syntax.SealedType
			c.checkPackageTags(decl.TagGroups, "TypeTags", supported)
			for _, field := range decl.Fields {
				c.checkPackageTags(field.TagGroups, "FieldTags", true)
			}
			for _, variant := range decl.Variants {
				c.checkPackageTags(variant.TagGroups, "VariantTags", true)
				for _, field := range variant.Fields {
					c.checkPackageTags(field.TagGroups, "FieldTags", true)
				}
			}
		}
	}
}

func (c *checker) checkPackageTags(groups []*syntax.TagGroup, name string, supported bool) {
	savedScopes := c.scopes
	defer func() { c.scopes = savedScopes }()
	for _, group := range groups {
		if group.Name == "go" {
			continue
		}
		if !supported {
			c.errorf(group.Pos, "tag groups can only be written on record or sealed types")
			continue
		}
		pkg := c.pkg.imports[group.Name]
		if group.Name == c.pkg.Name {
			pkg = c.pkg
		}
		if pkg == nil {
			c.errorf(group.Pos, "unknown tag package %s", group.Name)
			continue
		}
		if pkg != c.pkg {
			c.pkg.used[group.Name] = true
		}
		entry := pkg.types[name]
		if entry == nil {
			c.errorf(group.Pos, "package %s has no exported %s record for this tag group", group.Name, name)
			continue
		}
		record, ok := entry.typ.(*Record)
		if !ok || entry.decl.Kind != syntax.RecordType || len(record.TypeParams) != 0 {
			c.errorf(group.Pos, "%s.%s must be a non-generic record", group.Name, name)
			continue
		}
		closed := true
		for _, init := range group.Entries {
			if !isClosed(init.Value, c.closedConstructorCandidate) {
				c.errorf(init.Pos, "tag values must be closed values, like field defaults")
				closed = false
			}
		}
		if !closed {
			continue
		}
		writtenName := name
		if pkg != c.pkg {
			writtenName = group.Name + "." + name
		}
		head := &syntax.TypeExpr{Pos: group.Pos, Name: writtenName}
		c.info.assemblyTypes[head] = record
		literal := &syntax.RecordLit{Type: &syntax.TypeHead{Type: head, End: group.Pos}, Fields: group.Entries, End: group.End}
		c.scopes = []map[string]*local{{}}
		if c.sharedDefaults == nil {
			c.sharedDefaults = map[syntax.Expr]bool{}
		}
		c.sharedDefaults[literal] = true
		if c.exprWant(literal, record) == Invalid {
			continue
		}
		if !c.checkedClosedValue(literal) {
			c.errorf(group.Pos, "tag values must be closed values, like field defaults")
			continue
		}
		c.info.tagGroups[group] = literal
		// Run the same lowering and fact-proof boundary as a field default,
		// even when no derive request ever reads this declaration's tags.
		proof := &Field{Name: group.Name + " tag group", Pkg: c.pkg, Type: record, Decl: &syntax.FieldDecl{Pos: group.Pos, Default: literal}, defaultState: 2}
		c.info.fieldDefaults[proof] = literal
	}
}

// EditorTagFields exposes the same record selected by tag-body checking.
func EditorTagFields(info *Info, pos diag.Pos) []*Field {
	for group, literal := range info.tagGroups {
		if group.Pos.File == pos.File && group.Pos.Line == pos.Line && group.Pos.Col == pos.Col {
			if record, ok := info.types[literal].(*Record); ok {
				return record.Fields
			}
		}
	}
	return nil
}

// EditorTagType returns the checked type of a written tag value token.
func EditorTagType(info *Info, pos diag.Pos) Type {
	for group := range info.tagGroups {
		if group.Pos.File != pos.File || pos.Line < group.Pos.Line || pos.Line > group.End.Line || pos.Line == group.Pos.Line && pos.Col < group.Pos.Col || pos.Line == group.End.Line && pos.Col >= group.End.Col {
			continue
		}
		for expression, typ := range info.types {
			if expression.Position() == pos {
				return typ
			}
		}
	}
	return nil
}
