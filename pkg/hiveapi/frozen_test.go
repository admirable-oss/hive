package hiveapi_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/apisurface"
)

// TestSDKFrozen holds the Go SDK's exported declarations to
// api/v1/hiveapi.txt: types, their exported fields, functions and methods
// with their signatures, constants and variables.
func TestSDKFrozen(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	var lines []string
	text := func(n ast.Node) string {
		var b bytes.Buffer
		_ = printer.Fprint(&b, fset, n)
		return strings.Join(strings.Fields(b.String()), " ")
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() || (d.Recv != nil && !exportedRecv(d.Recv)) {
					continue
				}
				d.Body, d.Doc = nil, nil
				lines = append(lines, "func "+strings.TrimPrefix(text(d), "func "))
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if !s.Name.IsExported() {
							continue
						}
						if st, ok := s.Type.(*ast.StructType); ok {
							lines = append(lines, "type "+s.Name.Name+" struct")
							for _, fld := range st.Fields.List {
								for _, n := range fld.Names {
									if n.IsExported() {
										lines = append(lines, "field "+s.Name.Name+"."+n.Name+" "+text(fld.Type))
									}
								}
							}
							continue
						}
						s.Doc, s.Comment = nil, nil
						lines = append(lines, "type "+text(s))
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								kind := map[token.Token]string{token.CONST: "const", token.VAR: "var"}[d.Tok]
								lines = append(lines, kind+" "+n.Name)
							}
						}
					}
				}
			}
		}
	}
	if len(lines) == 0 {
		t.Fatal("found no exported declarations")
	}
	apisurface.Check(t, "../../api/v1/hiveapi.txt", lines)
}

func exportedRecv(r *ast.FieldList) bool {
	if len(r.List) == 0 {
		return false
	}
	t := r.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	id, ok := t.(*ast.Ident)
	return ok && id.IsExported()
}
