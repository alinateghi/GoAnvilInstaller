package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCommandsAndInterspersedFlags(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	run := func(args ...string) error { return runAt(context.Background(), root, args, &out, "test") }
	if err := run("init", "demo", "--mode=http", "--db", "sqlite"); err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "demo")
	if err := run("make:model", "Todo", "--fields", "Content:string,Done:bool"); err != nil {
		t.Fatal(err)
	}
	if err := run("make:migration", "--up", "--down", "--model=Todo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "database/migrations/up/000001_create_todos.sql")); err != nil {
		t.Fatal(err)
	}
	if err := run("make:model", "Todo"); err == nil {
		t.Fatal("duplicate model accepted")
	}
	for _, args := range [][]string{{"init", "--help"}, {"make:model", "--help"}, {"version"}, {"--help"}} {
		if err := run(args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"unknown"}, {"init"}, {"init", "--db=unknown", "other"}, {"init", "--mode=rpc", "other"}, {"make:migration"}, {"make:model", "todo"}, {"make:model", "Other", "--unknown"}} {
		if err := run(args...); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestCancellationDoesNotCreateProject(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	if err := runAt(ctx, root, []string{"init", "demo"}, &bytes.Buffer{}, "test"); err == nil {
		t.Fatal("cancellation ignored")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("files left after cancellation: %v, %v", entries, err)
	}
}
