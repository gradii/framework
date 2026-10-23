# Laravel processor and query grammar tests

Reference: `~/OpenSource/framework`, Laravel `12.x` at `0fc645c673`.
All 16 test methods in the five `Database*ProcessorTest.php` files and six
`Database*QueryGrammarTest.php` files are covered below.

| Upstream file in `tests/Database/` | Methods | Go coverage |
|---|---:|---|
| `DatabaseProcessorTest.php` | 1 | `processors/laravel_port_test.go`: `TestDatabaseProcessor_InsertGetIdProcessing` |
| `DatabaseMySqlProcessorTest.php` | 1 | `processors/laravel_port_test.go`: `TestDatabaseMySqlProcessor_ProcessColumns` |
| `DatabaseMariaDbProcessorTest.php` | 1 | `processors/laravel_port_test.go`: `TestDatabaseMariaDbProcessor_ProcessColumns` |
| `DatabasePostgresProcessorTest.php` | 1 | `processors/laravel_port_test.go`: `TestDatabasePostgresProcessor_ProcessColumns` |
| `DatabaseSQLiteProcessorTest.php` | 1 | `processors/laravel_port_test.go`: `TestDatabaseSQLiteProcessor_ProcessColumns` |
| `DatabaseQueryGrammarTest.php` | 4 | `grammars/laravel_port_test.go`: `TestDatabaseQueryGrammar_*` |
| `DatabaseMySqlQueryGrammarTest.php` | 1 | `grammars/laravel_port_test.go`: `TestDatabaseMySqlQueryGrammar_ToRawSql` |
| `DatabaseMariaDbQueryGrammarTest.php` | 1 | `grammars/laravel_port_test.go`: `TestDatabaseMariaDbQueryGrammar_ToRawSql` |
| `DatabasePostgresQueryGrammarTest.php` | 3 | `grammars/laravel_port_test.go`: `TestDatabasePostgresQueryGrammar_*` |
| `DatabaseSQLiteQueryGrammarTest.php` | 1 | `grammars/laravel_port_test.go`: `TestDatabaseSQLiteQueryGrammar_ToRawSql` |
| `DatabaseSqlServerQueryGrammarTest.php` | 1 | `grammars/laravel_port_test.go`: `TestDatabaseSqlServerQueryGrammar_ToRawSql` |

The column fixtures in `processors/testdata` were extracted from the upstream
`$listing` and `$expected` literals. Each fixture records its source method.
PHP array and `stdClass` rows use Go's `Row` map representation, and generated
integer IDs use `int64` to match `database/sql`.

The MariaDB fixture retains its upstream empty-string comment expectation.
Laravel's inherited MySQL implementation returns `null`, which PHPUnit's
`assertEquals` accepts as equal to an empty string. The Go test explicitly
normalizes this one expectation to `nil` before its strict comparison.

Additional Go regressions cover generated-column metadata, SQLite collations
and composite keys, SQL Server column types, default raw SQL escaping, and
concurrent registration of PostgreSQL operators.

Run the ports with:

```sh
go test ./database/query/grammars ./database/query/processors
go test -race ./database/query/grammars ./database/query/processors
```
