# External database drivers

The database runtimes are now `github.com/gradii/mysql`, `github.com/gradii/postgres`,
`github.com/gradii/sqlite`, and `github.com/gradii/sqlserver`.

Each driver supplies a native SQL connector, query grammar, result processor, and
complete schema compilation and metadata implementation. The framework retains
query state, model mapping, pool ownership, transaction handling, instrumentation,
and schema execution on the appropriate connection. It has no production imports
of the concrete driver packages.

`contracts/database/driver.Dialect` supplies connection/query components.
`Driver.Grammar()` supplies schema capabilities through `SchemaCompiler`,
`SchemaMetadataGrammar`, `SchemaOperationsGrammar`, and the planning/catalog
interfaces. These ordinary interfaces do not require generics.

Database identifiers come from explicit table/column descriptions. The driver
refactor does not restore automatic table naming from Go type names. Existing
`gorm:"column:..."` tags are parsed by the framework itself and do not introduce a
GORM dependency. Complete obfuscation of relationship field access is a separate
mapping concern.

Custom values use the standard `driver.Valuer` and `sql.Scanner` interfaces.
The framework's [JSON types and migration guide](types/README.md) describe NULL,
pointer receivers, and the supported serializer-tag boundary. Field traversal
uses the shared `internal/reflectx` helpers; JSON codecs are independent of the
driver packages.

Each logical connection alias has its own pool lifecycle. DB and ORM calls using
the same configured identity and logical name share resources; purging one alias
does not close another alias's pool. `DropAll*` resolves and validates namespaces
on the same leased writer session that performs the deletion.

DB/Schema nested transactions share one SQL transaction and physical connection,
using savepoints for nested rollback. Contexts carry cancellation and deadlines;
they do not identify or automatically propagate a transaction. Pass the returned
transaction handle to nested operations and execute nested scopes sequentially.
The manager's state mutex does not isolate concurrent goroutines sharing a
savepoint tree. ORM `BeginTransaction` currently reuses an existing transaction
without a savepoint, so its nested Commit is not an independent inner commit.
Unifying that ORM behavior and enforcing parallel-scope ownership require a
separate transaction change; the schema fixes use the DB/Tx implementation. See
the [transaction follow-up plan](TRANSACTION_FOLLOWUP_PLAN.md), which also records
the current stale-child rollback limitation.

## Tests

All original framework test files and test functions remain in the framework
repository. Test-only adapters select the external grammars and processors.
Expectations were ported where the new behavior intentionally differs, including
SQL Server character lengths, temporal precision and column comments, explicit
unsupported full-text indexes, and SQLite array-path updates.

Original driver configuration, schema, processor and connection test cases now
exercise the new APIs. The old GORM implementations are not retained.

The original driver database-tool tests were ported to the framework's independent
integration module:

| Original coverage | Current coverage in `tests/driver_fixture_test.go` |
| --- | --- |
| Build, configuration, inserting data, fresh database, shutdown | `TestDriverFixtureLifecycle` |
| Creating a separate database | `TestDriverFixtureDatabase` |
| Readiness with explicit writer settings or top-level settings | `TestDriverFixtureReady` |

The test fixtures use native connectors and manage only the databases they create.
They are test infrastructure; Docker and installation helpers are not driver
runtime features.

Additional regressions cover injected processors and grammar context changes,
pool aliases, optional version discovery, concurrent SQL Server version callbacks,
SQLite virtual-table shadow filtering, JSON validation, SQL Server JSON containment,
and SQLite array-element updates.

## Workspace and CI

Before the modules are published, use a local `go.work` containing the framework,
all four driver checkouts, and `framework/tests`. Keep local paths out of published
`go.mod` replacements. See [the CNB workspace instructions](../.cnb/README.md).

CNB explicitly checks out compatible revisions and generates its own workspace.
The `unit` mode runs all five root modules; `database` runs framework database tests;
`integration` runs the independent framework test module, including the ported
database-fixture cases. There are no old-code compatibility modules or legacy jobs.

The independent module also runs the driver review regressions for namespace
scope, SQLite rebuilds, generated/default/enum changes, and standard JSON values.
These execute through the CNB integration job rather than relying only on
environment-gated tests in the driver repositories.

Companion repository URLs and refs must be configured in CNB while the forks are
unpublished. A pipeline cannot fetch uncommitted changes from a developer's machine.
