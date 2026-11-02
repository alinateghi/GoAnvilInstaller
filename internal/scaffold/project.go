// Package scaffold creates GoAnvil applications without running shell commands.
package scaffold

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

//go:embed templates/*
var templates embed.FS

const FrameworkModule = "github.com/alinateghi/GoAnvil"

type Options struct {
	Name, Mode, Database, Module                     string
	FrameworkModule, FrameworkVersion, FrameworkPath string
	Address, DBHost, DBName, DBUser                  string
	DBPort                                           int
}

func DefaultOptions() Options {
	return Options{Mode: "http", Database: "mysql", FrameworkModule: FrameworkModule,
		FrameworkVersion: "v0.1.0", Address: ":8080", DBHost: "127.0.0.1", DBUser: "root"}
}

type Project struct {
	Schema           int    `json:"schema"`
	Module           string `json:"module"`
	FrameworkModule  string `json:"framework_module"`
	FrameworkVersion string `json:"framework_version"`
	Mode             string `json:"mode"`
	Database         string `json:"database"`
}

var projectName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
var modulePath = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z0-9.-]+(/[A-Za-z0-9_.-]+)+$`)
var moduleVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[A-Za-z0-9]+([.-][A-Za-z0-9]+)*)?(\+incompatible)?$`)

func validateModule(value string) bool {
	if !modulePath.MatchString(value) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." || strings.HasSuffix(part, ".") {
			return false
		}
	}
	return true
}

func (o *Options) validate(base string) error {
	if !projectName.MatchString(o.Name) {
		return errors.New("project name must start with a letter and contain only letters, digits, underscores, and hyphens")
	}
	if o.Mode != "http" {
		return errors.New("supported mode: http")
	}
	switch o.Database {
	case "postgresql", "pgx":
		o.Database = "postgres"
	case "sqlite3":
		o.Database = "sqlite"
	case "mssql":
		o.Database = "sqlserver"
	case "mysql", "postgres", "sqlite", "sqlserver":
	default:
		return errors.New("supported databases: mysql, postgres, sqlite, sqlserver")
	}
	if o.Module == "" {
		o.Module = "example.com/" + o.Name
	}
	if !validateModule(o.Module) || !validateModule(o.FrameworkModule) || o.Module == o.FrameworkModule {
		return errors.New("project and framework need distinct valid Go module paths with a domain")
	}
	if !moduleVersion.MatchString(o.FrameworkVersion) {
		return errors.New("framework version must be a concrete Go version such as v0.1.0")
	}
	major := strings.SplitN(strings.TrimPrefix(o.FrameworkVersion, "v"), ".", 2)[0]
	if major != "0" && major != "1" && !strings.HasSuffix(o.FrameworkModule, "/v"+major) && !strings.HasSuffix(o.FrameworkVersion, "+incompatible") {
		return errors.New("framework module path must end in /vMAJOR for versions v2 and newer")
	}
	if o.DBPort < 0 || o.DBPort > 65535 {
		return errors.New("database port must be between 0 and 65535")
	}
	if strings.TrimSpace(o.Address) == "" {
		return errors.New("HTTP address must not be empty")
	}
	if o.DBName == "" {
		o.DBName = strings.ToLower(strings.ReplaceAll(o.Name, "-", "_"))
		if o.Database == "sqlite" {
			o.DBName += ".db"
		}
	}
	if o.FrameworkPath != "" {
		if !filepath.IsAbs(o.FrameworkPath) {
			o.FrameworkPath = filepath.Join(base, o.FrameworkPath)
		}
		absolute, err := filepath.Abs(o.FrameworkPath)
		if err != nil {
			return err
		}
		o.FrameworkPath = filepath.ToSlash(absolute)
		data, err := os.ReadFile(filepath.Join(absolute, "go.mod"))
		if err != nil {
			return fmt.Errorf("read local framework go.mod: %w", err)
		}
		matched := false
		for _, line := range strings.Split(string(data), "\n") {
			words := strings.Fields(line)
			if len(words) == 2 && words[0] == "module" && strings.Trim(words[1], `"`) == o.FrameworkModule {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("local framework module does not match --framework-module")
		}
	}
	return nil
}

// Init stages a complete project before renaming it into place. Existing targets
// are refused, even if empty. No downloads or database connections are made.
func Init(ctx context.Context, base string, opts Options) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := opts.validate(base); err != nil {
		return nil, err
	}
	target := filepath.Join(base, opts.Name)
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("target already exists: %s", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	staging, err := os.MkdirTemp(base, ".anvil-init-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	files, err := render(opts)
	if err != nil {
		return nil, err
	}
	if _, err := writeFiles(ctx, staging, files); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Recheck because another process may have created an empty target.
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("target already exists: %s", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(staging, target); err != nil {
		return nil, err
	}
	return []string{target}, nil
}

func render(opts Options) (map[string][]byte, error) {
	entries, err := fs.ReadDir(templates, "templates")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		contents, err := templates.ReadFile("templates/" + entry.Name())
		if err != nil {
			return nil, err
		}
		pieces := strings.SplitN(string(contents), "\n", 2)
		if len(pieces) != 2 {
			return nil, fmt.Errorf("invalid template %s", entry.Name())
		}
		name := strings.TrimSpace(pieces[0])
		tmpl, err := template.New(name).Funcs(template.FuncMap{"quote": strconv.Quote, "env": envQuote}).Option("missingkey=error").Parse(pieces[1])
		if err != nil {
			return nil, err
		}
		var buffer bytes.Buffer
		if err := tmpl.Execute(&buffer, opts); err != nil {
			return nil, err
		}
		data := buffer.Bytes()
		if strings.HasSuffix(name, ".go") {
			data, err = format.Source(data)
			if err != nil {
				return nil, fmt.Errorf("format %s: %w", name, err)
			}
		}
		files[name] = data
	}
	manifest, err := json.MarshalIndent(Project{Schema: 1, Module: opts.Module, FrameworkModule: opts.FrameworkModule,
		FrameworkVersion: opts.FrameworkVersion, Mode: opts.Mode, Database: opts.Database}, "", "  ")
	if err != nil {
		return nil, err
	}
	files[".anvil.json"] = append(manifest, '\n')
	return files, nil
}

// dotenv double quotes expand dollars; use single quotes and escape their
// delimiter instead. Newlines are rejected to keep one setting per line.
func envQuote(s string) (string, error) {
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("configuration values must fit on one line")
	}
	if strings.Contains(s, "'") {
		return "", errors.New("configuration values must not contain single quotes; set them in .env after generation")
	}
	return "'" + s + "'", nil
}

func findProject(start string) (string, Project, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", Project{}, err
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, ".anvil.json"))
		if err == nil {
			var project Project
			if err := json.Unmarshal(data, &project); err != nil {
				return "", project, fmt.Errorf("invalid .anvil.json: %w", err)
			}
			if project.Schema != 1 || !validateModule(project.Module) || !validateModule(project.FrameworkModule) {
				return "", project, errors.New("unsupported or invalid .anvil.json")
			}
			switch project.Database {
			case "mysql", "postgres", "sqlite", "sqlserver":
			default:
				return "", project, errors.New("invalid database in .anvil.json")
			}
			return dir, project, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", Project{}, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", Project{}, errors.New("no GoAnvil project found; run inside a project created by anvil init")
		}
		dir = parent
	}
}

// writeFiles preflights every output and exclusively creates files. On failure
// only files created by this invocation are removed. Existing files are untouched.
func writeFiles(ctx context.Context, root string, files map[string][]byte) (created []string, err error) {
	for name := range files {
		if !fs.ValidPath(name) || path.IsAbs(name) {
			return nil, fmt.Errorf("invalid output path %q", name)
		}
		current := root
		for _, part := range strings.Split(name, "/") {
			current = filepath.Join(current, part)
			info, statErr := os.Lstat(current)
			if statErr == nil {
				if info.Mode()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("output path contains a symlink: %s", current)
				}
				if current == filepath.Join(root, filepath.FromSlash(name)) {
					return nil, fmt.Errorf("refusing to overwrite %s", current)
				}
				if !info.IsDir() {
					return nil, fmt.Errorf("output parent is not a directory: %s", current)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return nil, statErr
			}
		}
	}
	defer func() {
		if err != nil {
			for _, name := range created {
				_ = os.Remove(name)
			}
			created = nil
		}
	}()
	for name, contents := range files {
		if err = ctx.Err(); err != nil {
			return created, err
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return created, err
		}
		var file *os.File
		file, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return created, err
		}
		created = append(created, target)
		_, writeErr := file.Write(contents)
		err = errors.Join(writeErr, file.Close())
		if err != nil {
			return created, err
		}
	}
	return created, nil
}
