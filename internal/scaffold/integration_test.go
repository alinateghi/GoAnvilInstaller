package scaffold

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This opt-in test builds generated projects as standalone modules against a
// real framework checkout. No server database is required; SQLite runs for real.
func TestGeneratedProjectsEndToEnd(t *testing.T) {
	framework := os.Getenv("GOANVIL_TEST_FRAMEWORK")
	if framework == "" {
		t.Skip("set GOANVIL_TEST_FRAMEWORK to a GoAnvil checkout to build and run generated applications")
	}
	absolute, err := filepath.Abs(framework)
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []string{"mysql", "postgres", "sqlite", "sqlserver"} {
		t.Run(dialect, func(t *testing.T) {
			base := t.TempDir()
			opts := DefaultOptions()
			opts.Name, opts.Database, opts.FrameworkPath = "demo", dialect, absolute
			if _, err := Init(context.Background(), base, opts); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "demo")
			if _, err := Model(context.Background(), root, "Todo", "Content:string,Done:bool,DueAt:*time.Time"); err != nil {
				t.Fatal(err)
			}
			if _, err := Migration(context.Background(), root, "Todo", "", true, true); err != nil {
				t.Fatal(err)
			}
			var env []string
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if !strings.HasPrefix(key, "DB_") && key != "HTTP_ADDRESS" && key != "APP_ENV_FILE" && key != "GOWORK" {
					env = append(env, entry)
				}
			}
			env = append(env, "GOWORK=off", "APP_ENV_FILE=")
			run := func(command string, args ...string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Dir, cmd.Env = root, env
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s %v: %v\n%s", command, args, err, output)
				}
			}
			run("go", "mod", "tidy")
			run("go", "test", "./...")
			run("go", "vet", "./...")
			binary := filepath.Join(root, "demo")
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			run("go", "build", "-o", binary, ".")
			if dialect == "sqlite" {
				for _, direction := range []string{"up", "up", "down", "down", "up"} {
					run(binary, "migrate", direction)
				}
				// Exercise the emitted SQL, not just its text or its compilation.
				source := fmt.Sprintf(`package main
import (
 "context"
 "testing"
 "github.com/alinateghi/GoAnvil/config"
 "github.com/alinateghi/GoAnvil/database/sqldb"
)
func TestGeneratedSQLiteSchema(t *testing.T) {
 opts := config.Defaults().Database
 opts.Driver, opts.Name = "sqlite", %q
 connection, err := (sqldb.Connector{}).Open(context.Background(), opts)
 if err != nil { t.Fatal(err) }; defer connection.SQL.Close()
 if _, err := connection.SQL.Exec("INSERT INTO todos (content, done) VALUES (?, ?)", "generated", false); err != nil { t.Fatal(err) }
 var count int
 if err := connection.SQL.QueryRow("SELECT COUNT(*) FROM goanvil_migrations").Scan(&count); err != nil || count != 1 { t.Fatalf("ledger count = %%d, %%v", count, err) }
 var content string
 if err := connection.SQL.QueryRow("SELECT content FROM todos WHERE id=1").Scan(&content); err != nil || content != "generated" { t.Fatalf("content = %%s, %%v", content, err) }
}
`, "demo.db")
				if err := os.WriteFile(filepath.Join(root, "sqlite_integration_test.go"), []byte(source), 0644); err != nil {
					t.Fatal(err)
				}
				run("go", "test", "./...")
			}
		})
	}
}
