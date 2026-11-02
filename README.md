# GoAnvilInstaller

The `anvil` developer CLI creates HTTP projects using
[`github.com/alinateghi/GoAnvil`](https://github.com/alinateghi/GoAnvil).
It also generates Go models and dialect-specific initial SQL migrations.
Requires Go 1.24 or newer. Templates are embedded in the executable; generation
does not run shell commands, download dependencies, or connect to a database.

## Install

From a local checkout:

```sh
go install ./cmd/anvil
```

After publishing this repository:

```sh
go install github.com/alinateghi/GoAnvilInstaller/cmd/anvil@latest
```

Add `GOBIN` (or `GOPATH/bin`) to PATH. The executable is `anvil` (`anvil.exe` on
Windows). `go anvil` is not a Go subcommand. There is no APT package repository
yet; `go install` is the supported installation method.

## Initialize

```sh
anvil init --mode=http --db=mysql --module=github.com/alinateghi/myapp myapp
cd myapp
# Copy .env.example to .env and configure credentials; create the server database.
go mod tidy
go run . migrate up
go run . serve
```

The starter includes an HTTP `/health` route, configuration, command wiring,
graceful shutdown, database scopes, a migration command, application package
directories, and a route test. It does not generate todo/CRUD implementations.
Models and application code import the framework as a separate module.

| Flag | Default / purpose |
| --- | --- |
| --mode | `http` (currently the only mode) |
| --db | `mysql`; also postgres, sqlite, sqlserver and their documented driver aliases |
| --module | `example.com/PROJECT_NAME`; use your repository path for real projects |
| --framework-module | `github.com/alinateghi/GoAnvil` |
| --framework-version | `v0.1.0`; must exist remotely unless using a local replacement |
| --framework-path | Optional local checkout; generates an absolute replace directive |
| --address | `:8080` |
| --db-host | `127.0.0.1` |
| --db-port | `0` (driver default) |
| --db-name | Project name with hyphens replaced by underscores; SQLite adds `.db` |
| --db-user | `root`; use `.env` for passwords |

Flags may come before or after the project/model name. Project names start with
an ASCII letter and contain letters, digits, underscores, or hyphens. Init creates
a new directory beneath the current directory and refuses existing directories.

For local development before publishing:

```sh
anvil init --db=sqlite --framework-path=../GoAnvil myapp
```

The framework path is resolved relative to where init runs, then recorded in
go.mod. Move to a published dependency by dropping the replace directive and
selecting a real framework version. Generated projects inside another Go workspace
need their own workspace entry or `GOWORK=off` when running Go commands.

## Models

```sh
anvil make:model Todo
anvil make:model Task --fields=Content:string,Done:bool,DueAt:*time.Time
```

Creates `models/todo.go` with `ID int64`, or a Task model with the additional
fields. Field types: string, bool, int, int64, uint64, float64, time.Time, []byte,
and nullable pointers to these. Model and field names must be exported ASCII Go
identifiers. JSON tags use snake_case. A type already present in models is refused.
No database table, route, service, repository, or ORM mapping is created here.

## Migrations

```sh
anvil make:migration --up --down --model=Todo
anvil make:migration --model=Task --table=tasks
```

Reads the existing Go model and writes matching numbered files under
`database/migrations/up/` and `down/`. With neither direction specified, both are
generated; selecting only one creates only that file, and the missing counterpart
must be supplied manually before rollback. Files are named
`000001_create_todos.sql` in both directories. A second CREATE for the same table
is refused. Default table naming is snake_case plus `s`; specify irregular names
explicitly. This is initial schema generation, not an ALTER/schema diff tool.

The database comes from `.anvil.json`, written by init. SQL uses dialect-specific
identity columns, quoting, scalar types, and nullability. SQLite uint64 is refused
because its integer representation is signed. Model ID must be int or int64.
Tags do not customize SQL column names. Unsupported types and embedded fields
need manually written migrations. Add relationships, indexes, constraints,
defaults, and schema changes yourself after reviewing the generated SQL.

`go run . migrate up` applies pending files using the framework's applied-filename
ledger. `go run . migrate down` reverses **all** applied files, dropping their tables
and data. Down filenames must match up filenames. Run one migrator at a time;
there is no cross-process lock. Transactional DDL depends on the database engine,
and MySQL may commit DDL even if the migration fails. No checksums are recorded.
Changing DB_DRIVER does not translate previously generated migrations.

Both generators locate `.anvil.json` from the current directory or a parent.
Existing files are never intentionally overwritten; conflicting outputs fail
before generation and write errors remove files created by that invocation.

## Development and verification

```sh
go test ./...
go vet ./...
go build ./...
go run ./cmd/anvil --help
```

Optional end-to-end tests: set `GOANVIL_TEST_FRAMEWORK` to an absolute framework
checkout path, then run `go test ./internal/scaffold -run EndToEnd -v`. They
generate all four database variants with GOWORK disabled, download/resolve module
dependencies, test/vet/build the emitted projects, and run SQLite migrations and
schema queries. Live MySQL, PostgreSQL, and SQL Server are not exercised.

Release builds can set the CLI version using:

```sh
go build -ldflags="-X main.version=v0.1.0" -o anvil ./cmd/anvil
```
