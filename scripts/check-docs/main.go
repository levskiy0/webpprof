// Command check-docs verifies that public Go packages document their exported API.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type finding struct {
	position token.Position
	name     string
	kind     string
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	moduleDirs, err := findModuleDirs(root)
	if err != nil {
		fatal(err)
	}

	var findings []finding
	for _, moduleDir := range moduleDirs {
		moduleFindings, err := inspectModule(moduleDir)
		if err != nil {
			fatal(err)
		}
		findings = append(findings, moduleFindings...)
	}

	sort.Slice(findings, func(i, j int) bool {
		left := findings[i].position
		right := findings[j].position
		if left.Filename != right.Filename {
			return left.Filename < right.Filename
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		return findings[i].name < findings[j].name
	})

	if len(findings) == 0 {
		fmt.Println("all exported declarations are documented")
		return
	}

	for _, item := range findings {
		path, err := filepath.Rel(root, item.position.Filename)
		if err != nil {
			path = item.position.Filename
		}
		fmt.Printf("%s:%d: exported %s %s has no doc comment\n", path, item.position.Line, item.kind, item.name)
	}
	os.Exit(1)
}

func findModuleDirs(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		base := entry.Name()
		if base == ".git" || base == "vendor" || base == "var" {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
			dirs = append(dirs, path)
			if path != root {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return dirs, err
}

func inspectModule(moduleDir string) ([]finding, error) {
	var findings []finding
	type packageDoc struct {
		position   token.Position
		name       string
		documented bool
	}
	packages := make(map[string]packageDoc)
	err := filepath.WalkDir(moduleDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != moduleDir {
				base := entry.Name()
				if base == ".git" || base == "vendor" || base == "var" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"internal"+string(filepath.Separator)) {
			return nil
		}

		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if file.Name.Name == "main" {
			return nil
		}
		packageKey := filepath.Dir(path) + ":" + file.Name.Name
		info, ok := packages[packageKey]
		if !ok {
			info = packageDoc{position: fileSet.Position(file.Package), name: file.Name.Name}
		}
		info.documented = info.documented || file.Doc != nil
		packages[packageKey] = info
		findings = append(findings, inspectFile(fileSet, file)...)
		return nil
	})
	for _, info := range packages {
		if !info.documented {
			findings = append(findings, finding{position: info.position, name: info.name, kind: "package"})
		}
	}
	return findings, err
}

func inspectFile(fileSet *token.FileSet, file *ast.File) []finding {
	var findings []finding
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Name.IsExported() && receiverIsExported(declaration.Recv) && declaration.Doc == nil {
				kind := "function"
				if declaration.Recv != nil {
					kind = "method"
				}
				findings = append(findings, finding{position: fileSet.Position(declaration.Name.Pos()), name: declaration.Name.Name, kind: kind})
			}
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.TypeSpec:
					if specification.Name.IsExported() && declaration.Doc == nil && specification.Doc == nil {
						findings = append(findings, finding{position: fileSet.Position(specification.Name.Pos()), name: specification.Name.Name, kind: "type"})
					}
				case *ast.ValueSpec:
					if declaration.Doc != nil || specification.Doc != nil {
						continue
					}
					for _, name := range specification.Names {
						if name.IsExported() {
							findings = append(findings, finding{position: fileSet.Position(name.Pos()), name: name.Name, kind: declaration.Tok.String()})
						}
					}
				}
			}
		}
	}
	return findings
}

func receiverIsExported(receiver *ast.FieldList) bool {
	if receiver == nil {
		return true
	}
	if len(receiver.List) == 0 {
		return false
	}
	return exportedTypeName(receiver.List[0].Type)
}

func exportedTypeName(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.IsExported()
	case *ast.StarExpr:
		return exportedTypeName(expression.X)
	case *ast.IndexExpr:
		return exportedTypeName(expression.X)
	case *ast.IndexListExpr:
		return exportedTypeName(expression.X)
	default:
		return false
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
