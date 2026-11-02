// Package cli implements the anvil developer command.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alinateghi/GoAnvilInstaller/internal/scaffold"
)

const help = `GoAnvil project tools

Usage:
  anvil init [options] PROJECT_NAME
  anvil make:model [--fields=Content:string,Done:bool] Todo
  anvil make:migration [--up] [--down] --model=Todo [--table=todos]
  anvil version

Run any command with --help for its options. Flags may precede or follow names.
Project generators find .anvil.json in the current directory or its parents.
`

func Run(ctx context.Context, args []string, out io.Writer, version string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return runAt(ctx, dir, args, out, version)
}

func runAt(ctx context.Context, dir string, args []string, out io.Writer, version string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprint(out, help)
		return err
	}
	if args[0] == "version" || args[0] == "--version" {
		_, err := fmt.Fprintln(out, "anvil", version)
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	var generate func() ([]string, error)
	switch args[0] {
	case "init":
		opts := scaffold.DefaultOptions()
		flags.StringVar(&opts.Mode, "mode", opts.Mode, "project mode: http")
		flags.StringVar(&opts.Database, "db", opts.Database, "mysql, postgres, sqlite, or sqlserver")
		flags.StringVar(&opts.Module, "module", "", "project Go module path (default example.com/PROJECT_NAME)")
		flags.StringVar(&opts.FrameworkModule, "framework-module", opts.FrameworkModule, "GoAnvil module path")
		flags.StringVar(&opts.FrameworkVersion, "framework-version", opts.FrameworkVersion, "GoAnvil release version")
		flags.StringVar(&opts.FrameworkPath, "framework-path", "", "local GoAnvil checkout; writes a replace directive")
		flags.StringVar(&opts.Address, "address", opts.Address, "HTTP listen address")
		flags.StringVar(&opts.DBHost, "db-host", opts.DBHost, "database host")
		flags.IntVar(&opts.DBPort, "db-port", 0, "database port (0 uses driver default)")
		flags.StringVar(&opts.DBName, "db-name", "", "database name (or SQLite filename)")
		flags.StringVar(&opts.DBUser, "db-user", opts.DBUser, "database username; set passwords in .env")
		generate = func() ([]string, error) {
			if flags.NArg() != 1 {
				return nil, errors.New("usage: anvil init [options] PROJECT_NAME")
			}
			opts.Name = flags.Arg(0)
			return scaffold.Init(ctx, dir, opts)
		}
	case "make:model":
		fields := flags.String("fields", "", "comma-separated exported Go fields, e.g. Content:string,Done:bool")
		generate = func() ([]string, error) {
			if flags.NArg() != 1 {
				return nil, errors.New("usage: anvil make:model [--fields=Content:string] Todo")
			}
			return scaffold.Model(ctx, dir, flags.Arg(0), *fields)
		}
	case "make:migration":
		model := flags.String("model", "", "existing exported model type, e.g. Todo")
		table := flags.String("table", "", "SQL table name (default snake_case model name + s)")
		up := flags.Bool("up", false, "generate up SQL")
		down := flags.Bool("down", false, "generate down SQL")
		generate = func() ([]string, error) {
			if flags.NArg() != 0 || *model == "" {
				return nil, errors.New("usage: anvil make:migration [--up] [--down] --model=Todo")
			}
			return scaffold.Migration(ctx, dir, *model, *table, *up, *down)
		}
	default:
		return fmt.Errorf("unknown command %q; run anvil --help", args[0])
	}
	if err := parse(flags, args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	paths, err := generate()
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, err := fmt.Fprintln(out, "Created", path); err != nil {
			return err
		}
	}
	if args[0] == "init" {
		_, err = fmt.Fprintf(out, "\nNext: cd %s, copy .env.example to .env, then run go mod tidy and go run . serve.\n", flags.Arg(0))
	}
	return err
}

// parse permits flags after positional names while retaining flag's validation.
func parse(fs *flag.FlagSet, args []string) error {
	var options, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		options = append(options, arg)
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		f := fs.Lookup(name)
		if f == nil || hasValue {
			continue
		}
		boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
		if ok && boolean.IsBoolFlag() {
			continue
		}
		if i+1 == len(args) {
			return fmt.Errorf("flag needs an argument: --%s", name)
		}
		i++
		options = append(options, args[i])
	}
	return fs.Parse(append(append(options, "--"), positional...))
}
