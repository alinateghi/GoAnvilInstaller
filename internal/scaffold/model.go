package scaffold

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type field struct {
	Name, Type string
	Nullable   bool
}

var sqlName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func exportedName(name string) bool {
	return token.IsIdentifier(name) && ast.IsExported(name) && regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`).MatchString(name)
}

func snake(name string) string {
	runes := []rune(name)
	var result strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			result.WriteByte('_')
		}
		result.WriteRune(unicode.ToLower(r))
	}
	return result.String()
}

func supportedType(value string) bool {
	switch strings.TrimPrefix(value, "*") {
	case "string", "bool", "int", "int64", "uint64", "float64", "time.Time", "[]byte":
		return true
	}
	return false
}

func parseFields(input string) ([]field, error) {
	fields := []field{{Name: "ID", Type: "int64"}}
	seen := map[string]bool{"id": true}
	if input == "" {
		return fields, nil
	}
	for _, spec := range strings.Split(input, ",") {
		name, typ, ok := strings.Cut(strings.TrimSpace(spec), ":")
		if !ok || !exportedName(name) || !supportedType(typ) {
			return nil, fmt.Errorf("invalid field %q; use ExportedName:type (string, bool, int, int64, uint64, float64, time.Time, []byte; optional * prefix)", spec)
		}
		column := snake(name)
		if seen[column] {
			return nil, fmt.Errorf("duplicate field or SQL column %q", name)
		}
		seen[column] = true
		fields = append(fields, field{Name: name, Type: typ, Nullable: strings.HasPrefix(typ, "*")})
	}
	return fields, nil
}

func Model(ctx context.Context, start, name, input string) ([]string, error) {
	if !exportedName(name) {
		return nil, errors.New("model must be an exported Go identifier using ASCII letters and digits, e.g. Todo")
	}
	root, _, err := findProject(start)
	if err != nil {
		return nil, err
	}
	fields, err := parseFields(input)
	if err != nil {
		return nil, err
	}
	// Also reject a type with the same name in another model filename.
	existing, err := readModel(root, name)
	if err == nil && len(existing) > 0 {
		return nil, fmt.Errorf("model %s already exists", name)
	}
	if err != nil && !errors.Is(err, errModelNotFound) {
		return nil, err
	}
	var source strings.Builder
	source.WriteString("package models\n\n")
	for _, f := range fields {
		if strings.TrimPrefix(f.Type, "*") == "time.Time" {
			source.WriteString("import \"time\"\n\n")
			break
		}
	}
	fmt.Fprintf(&source, "// %s is an application model.\ntype %s struct {\n", name, name)
	for _, f := range fields {
		fmt.Fprintf(&source, "%s %s `json:%q`\n", f.Name, f.Type, snake(f.Name))
	}
	source.WriteString("}\n")
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return nil, err
	}
	return writeFiles(ctx, root, map[string][]byte{"models/" + snake(name) + ".go": formatted})
}

var errModelNotFound = errors.New("model not found")

func readModel(root, name string) ([]field, error) {
	entries, err := os.ReadDir(filepath.Join(root, "models"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "models", entry.Name()), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse model: %w", err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typ := spec.(*ast.TypeSpec)
				if typ.Name.Name != name {
					continue
				}
				structure, ok := typ.Type.(*ast.StructType)
				if !ok {
					return nil, fmt.Errorf("model %s is not a struct", name)
				}
				var fields []field
				seen := map[string]bool{}
				for _, f := range structure.Fields.List {
					if len(f.Names) == 0 {
						return nil, errors.New("embedded model fields need a manually written migration")
					}
					var typText strings.Builder
					if err := format.Node(&typText, token.NewFileSet(), f.Type); err != nil {
						return nil, err
					}
					value := typText.String()
					if !supportedType(value) {
						return nil, fmt.Errorf("unsupported model field type %s; write a custom migration", value)
					}
					// A selector named time.Time must refer to the standard time package.
					if strings.TrimPrefix(value, "*") == "time.Time" {
						validTime := false
						for _, imp := range file.Imports {
							if imp.Path.Value == `"time"` && (imp.Name == nil || imp.Name.Name == "time") {
								validTime = true
							}
						}
						if !validTime {
							return nil, errors.New("time.Time must import the standard time package")
						}
					}
					for _, ident := range f.Names {
						if !exportedName(ident.Name) {
							return nil, errors.New("model fields must be exported ASCII identifiers")
						}
						column := snake(ident.Name)
						if seen[column] {
							return nil, fmt.Errorf("duplicate SQL column %s", column)
						}
						seen[column] = true
						fields = append(fields, field{Name: ident.Name, Type: value, Nullable: strings.HasPrefix(value, "*")})
					}
				}
				return fields, nil
			}
		}
	}
	return nil, errModelNotFound
}

// Migration creates a CREATE TABLE / DROP TABLE pair from the current model.
// With neither direction selected, both are generated. Later schema changes
// require hand-authored ALTER migrations; no database introspection is performed.
func Migration(ctx context.Context, start, model, table string, up, down bool) ([]string, error) {
	if !exportedName(model) {
		return nil, errors.New("--model must name an exported Go struct")
	}
	root, project, err := findProject(start)
	if err != nil {
		return nil, err
	}
	fields, err := readModel(root, model)
	if err != nil {
		return nil, fmt.Errorf("read model %s: %w", model, err)
	}
	if table == "" {
		table = snake(model) + "s"
	}
	if !sqlName.MatchString(table) || len(table) > 63 {
		return nil, errors.New("table must use lowercase ASCII letters, digits, underscores; maximum 63 characters")
	}
	upSQL, downSQL, err := schemaSQL(project.Database, table, fields)
	if err != nil {
		return nil, err
	}
	if !up && !down {
		up, down = true, true
	}
	var highest uint64
	for _, direction := range []string{"up", "down"} {
		entries, err := os.ReadDir(filepath.Join(root, "database", "migrations", direction))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			prefix, _, ok := strings.Cut(entry.Name(), "_")
			if !ok {
				return nil, fmt.Errorf("invalid existing migration name %q", entry.Name())
			}
			n, err := strconv.ParseUint(prefix, 10, 64)
			if err != nil || n >= 999999 {
				return nil, errors.New("migration sequences must be between 000001 and 999998")
			}
			if n > highest {
				highest = n
			}
			if strings.HasSuffix(entry.Name(), "_create_"+table+".sql") {
				return nil, fmt.Errorf("a create migration for %s already exists; write an ALTER migration for schema changes", table)
			}
		}
	}
	name := fmt.Sprintf("%06d_create_%s.sql", highest+1, table)
	files := map[string][]byte{}
	if up {
		files["database/migrations/up/"+name] = []byte(upSQL)
	}
	if down {
		files["database/migrations/down/"+name] = []byte(downSQL)
	}
	paths, err := writeFiles(ctx, root, files)
	sort.Strings(paths)
	return paths, err
}

func schemaSQL(dialect, table string, fields []field) (string, string, error) {
	quote := func(name string) string { return `"` + name + `"` }
	if dialect == "mysql" {
		quote = func(name string) string { return "`" + name + "`" }
	}
	if dialect == "sqlserver" {
		quote = func(name string) string { return "[" + name + "]" }
	}
	var lines []string
	hasID := false
	for _, f := range fields {
		if f.Name == "ID" {
			if f.Type != "int64" && f.Type != "int" {
				return "", "", errors.New("auto-generated ID must have type int or int64")
			}
			hasID = true
			identity := map[string]string{"mysql": "BIGINT AUTO_INCREMENT PRIMARY KEY", "postgres": "BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY", "sqlite": "INTEGER PRIMARY KEY AUTOINCREMENT", "sqlserver": "BIGINT IDENTITY(1,1) PRIMARY KEY"}[dialect]
			if identity == "" {
				return "", "", errors.New("unsupported SQL dialect")
			}
			lines = append(lines, "    "+quote("id")+" "+identity)
			continue
		}
		column := snake(f.Name)
		if len(column) > 63 {
			return "", "", errors.New("column names must not exceed 63 characters")
		}
		typ, err := sqlType(dialect, strings.TrimPrefix(f.Type, "*"))
		if err != nil {
			return "", "", err
		}
		nullable := " NOT NULL"
		if f.Nullable {
			nullable = " NULL"
		}
		lines = append(lines, "    "+quote(column)+" "+typ+nullable)
	}
	if !hasID {
		return "", "", errors.New("model needs an ID int64 field for an automatic create migration")
	}
	return "CREATE TABLE " + quote(table) + " (\n" + strings.Join(lines, ",\n") + "\n);\n", "DROP TABLE " + quote(table) + ";\n", nil
}

func sqlType(dialect, typ string) (string, error) {
	types := map[string]map[string]string{
		"mysql":     {"string": "TEXT", "bool": "BOOLEAN", "int": "BIGINT", "int64": "BIGINT", "uint64": "BIGINT UNSIGNED", "float64": "DOUBLE", "time.Time": "DATETIME(6)", "[]byte": "LONGBLOB"},
		"postgres":  {"string": "TEXT", "bool": "BOOLEAN", "int": "BIGINT", "int64": "BIGINT", "uint64": "NUMERIC(20,0)", "float64": "DOUBLE PRECISION", "time.Time": "TIMESTAMPTZ", "[]byte": "BYTEA"},
		"sqlite":    {"string": "TEXT", "bool": "BOOLEAN", "int": "INTEGER", "int64": "INTEGER", "float64": "REAL", "time.Time": "DATETIME", "[]byte": "BLOB"},
		"sqlserver": {"string": "NVARCHAR(MAX)", "bool": "BIT", "int": "BIGINT", "int64": "BIGINT", "uint64": "DECIMAL(20,0)", "float64": "FLOAT", "time.Time": "DATETIMEOFFSET", "[]byte": "VARBINARY(MAX)"},
	}
	value := types[dialect][typ]
	if value == "" {
		return "", fmt.Errorf("type %s is not supported for %s migrations (SQLite uint64 requires a custom storage strategy)", typ, dialect)
	}
	return value, nil
}
