# Laravel Query Builder parity checklist

**Purpose.** This file records the compiler contracts and public database integration for the
[approved port scope](../LARAVEL_PORT_PLAN.md). Execution and platform evidence are recorded in
[the database test mapping](../LARAVEL_DB_TESTS.md); compiler coverage alone is not proof of runtime parity.

**Source of truth** (Laravel checkout at `/Users/dog/OpenSource/framework`):

| Upstream component | Go implementation |
|---|---|
| `Query/Builder.php`, `Query/JoinClause.php` | `database/query`, public adapter `database/db` |
| `Grammar.php`, `Query/Grammars/*Grammar.php` | `database/query/grammars` |
| `Query/Processors/*Processor.php` | `database/query/processors` |
| `Connection.php`, `Concerns/ManagesTransactions.php` | `database/db`, `database/driver` |
| `Schema/Builder.php`, `Schema/Blueprint.php`, `Schema/Grammars/*Grammar.php` | `database/schema`, existing v1.18 driver grammars |

The fluent builder lives in `database/query`, shared query types and value utilities in
`database/query/core`, SQL compilers in `database/query/grammars`, and result processors in
`database/query/processors`. `core` has no compiler or builder dependency. Builders and grammars
call helpers such as `core.SortedKeys` directly; compatibility aliases only preserve public APIs.
Query descriptors and constants are referenced directly as `core.Where`, `core.UpdateValues`,
`core.BooleanAnd`, etc.; the former `query/types.go` re-export layer has been removed.
`NewBuilder(grammar, processor)` mirrors
Laravel's builder wiring: `InsertGetID` compiles and cleans bindings before delegating execution
to the connection's processor. DB and ORM adapters supply writer access, tracing, and model key
conversion. The grammar reads a compilation-state interface, so it does not import the builder
package. Existing query grammar constructors remain available through compatibility aliases.

## 1. Component order

`Query/Grammars/Grammar.php::$selectComponents`, compiled in exactly this order:

```
aggregate, columns, from, indexHint, joins, wheres, groups, havings, orders, limit, offset, lock
```

`compileSelect` short-circuits to `compileUnionAggregate` when `unions || havings` and an aggregate
is set, and to `compileGroupLimit` when `groupLimit` is set. In Go the group limit is carried by the
`groupLimitSet` presence flag (`Builder::groupLimit()` is ported as `Builder.GroupLimit()`), and the
short-circuit sets `columns` to `['*']` when unset before handing off, matching Laravel. Otherwise it
defaults `columns` to `['*']`, concatenates the components, then appends
`wrapUnion(sql) + ' ' + compileUnions(query)` when unions exist, and restores the original
`columns`.

`compileComponents` only compiles a component when it is set. In Go, presence must be explicit
(nil pointer or a presence flag), because every struct field always exists.

## 2. Binding keys and flatten order

`Query/Builder.php::$bindings`, flattened in this key order by `getBindings()`:

```
select, from, join, where, groupBy, having, order, union, unionOrder
```

`addBinding(value, type)`: rejects unknown keys, appends `castBinding(value)`, and for a slice merges
then re-indexes. `cleanBindings` drops Expressions and casts the rest. `castBinding` unwraps enums.
`flattenValue` collapses an array to its first scalar.

## 3. Where type table

Dispatch is `$where['boolean'] . ' ' . $this->{"where{$where['type']}"}($query, $where)`, joined, with
the leading boolean stripped by `removeLeadingBoolean` (`preg_replace('/and |or /i', $value, 1)`).
`concatenateWhereClauses` prefixes `on ` for a `JoinClause` and `where ` otherwise.

| Type | Builder entry | Stored fields | Compiler |
|---|---|---|---|
| Basic | where | type, column, operator, value, boolean | whereBasic |
| Bitwise | where | type, column, operator, value, boolean | whereBitwise |
| JsonBoolean | where on a `->` path with bool value | type, column, operator, value, boolean | whereJsonBoolean |
| Column | whereColumn | type, first, operator, second, boolean | whereColumn |
| raw | whereRaw | type, sql, boolean | whereRaw |
| In | whereIn | type, column, values, boolean | whereIn |
| NotIn | whereNotIn | type, column, values, boolean | whereNotIn |
| InRaw | whereIntegerInRaw | type, column, values, boolean | whereInRaw |
| NotInRaw | whereIntegerNotInRaw | type, column, values, boolean | whereNotInRaw |
| Null | whereNull | type, column, boolean | whereNull |
| NotNull | whereNotNull | type, column, boolean | whereNotNull |
| between | whereBetween | type, column, values, boolean, not | whereBetween |
| betweenColumns | whereBetweenColumns | type, column, values, boolean, not | whereBetweenColumns |
| valueBetween | whereValueBetween | type, value, columns, boolean, not | whereValueBetween |
| Nested | whereNested | type, query (child Builder), boolean | whereNested |
| Sub | whereSub | type, column, operator, query, boolean | whereSub |
| Exists | whereExists | type, query, boolean | whereExists |
| NotExists | whereNotExists | type, query, boolean | whereNotExists |
| RowValues | whereRowValues | type, columns, operator, values, boolean | whereRowValues |
| JsonContains | whereJsonContains | type, column, value, boolean, not | whereJsonContains |
| JsonContainsKey | whereJsonContainsKey | type, column, boolean, not | whereJsonContainsKey |
| JsonLength | whereJsonLength | type, column, operator, value, boolean | whereJsonLength |
| JsonOverlaps | whereJsonOverlaps | type, column, value, boolean, not | whereJsonOverlaps |
| Fulltext | whereFullText | type, columns, value, options, boolean | whereFullText |
| Date | whereDate | column, type, boolean, operator, value | whereDate |
| Time | whereTime | column, type, boolean, operator, value | whereTime |
| Day | whereDay | column, type, boolean, operator, value | whereDay |
| Month | whereMonth | column, type, boolean, operator, value | whereMonth |
| Year | whereYear | column, type, boolean, operator, value | whereYear |
| Like | whereLike | type, column, value, caseSensitive, boolean, not | whereLike |
| Expression | where(Expression) | type, column, boolean | whereExpression |

Negation lives in the `boolean` connector as `and not` / `or not`, never as a separate flag.

## 4. Base Grammar (`Illuminate\Database\Grammar`)

wrapArray, wrapTable, wrap, wrapAliasedValue, wrapAliasedTable, wrapSegments, wrapValue,
wrapJsonSelector, isJsonSelector, columnize, parameterize, parameter, quoteString, escape,
isExpression, getValue, getDateFormat, getTablePrefix, setTablePrefix

Shapes to reproduce exactly:

- `parameter(v)` returns `?` unless `v` is an Expression, then returns its raw value.
- `parameterize([])` joins `parameter(...)` with `, `.
- `columnize([])` joins `wrap(...)` with `, `.
- `wrap(v)`: Expression passthrough; `as` alias to wrapAliasedValue; JSON selector to
  wrapJsonSelector; else `wrapSegments(explode('.', v))`.
- `wrapTable(t)`: Expression passthrough; `as` alias to wrapAliasedTable; schema-qualified inserts
  the prefix before the last segment; else `wrapValue(prefix . t)`.
- `whereNested` strips 6 characters for a normal query and 3 for a JoinClause, because the child
  compiles to `where ...` or `on ...`.
- `whereIn` with no values compiles to `0 = 1`; `whereNotIn` with no values compiles to `1 = 1`.
- `whereLike` on the base throws for a case-sensitive match; MySQL maps to `like binary`.

## 5. Query Grammar (`Illuminate\Database\Query\Grammars\Grammar`)

compileSelect, compileComponents, compileAggregate, compileColumns, compileFrom, compileJoins,
compileWheres, compileWheresToArray, concatenateWhereClauses, compileGroups, compileHavings,
compileHaving (+ Between, Null, Nested, Basic), compileOrders, compileOrdersToArray, compileLimit,
compileOffset, compileLock, compileRandom, compileUnions, compileUnion, wrapUnion,
compileUnionAggregate, compileExists, compileInsert, compileInsertUsing, compileInsertGetId,
compileInsertOrIgnore, compileUpdate, compileUpdateColumns, compileUpdateWithoutJoins,
compileUpsert, prepareBindingsForUpdate, compileDelete, compileDeleteWithoutJoins, compileTruncate,
dateBasedWhere, concatenate, removeLeadingBoolean

Plus the 31 `where*` compilers in section 3. `compileIndexHint` exists only in dialects; the base
must provide a no-op so an unset component is harmless.

Write-side shapes:

- `compileInsert`: empty values to `insert into t default values`; else
  `insert into t (cols) values (?, ?), (?, ?)`.
- `compileInsertGetId` delegates to `compileInsert` on the base; Postgres appends `returning "id"`.
- `compileUpdate`: `update t set c = ?, ... where ...`, with a joins variant.
- `compileUpsert` throws on the base; each dialect implements it.
- `compileDelete`: `delete from t where ...`, with a joins variant.
- `compileTruncate` returns `truncate table t` (a statement list, not a string). Postgres appends
  `restart identity cascade` by default, and `query.CascadeOnTruncate(false)` drops the `cascade` keyword,
  mirroring Laravel's static `$cascadeTruncate`; the flag is process-wide.
- `compileExists`: `select exists(<select>) as "exists"`.

## 6. Per-dialect overrides

Each dialect extends the base and overrides only these.

**MySqlGrammar**
whereLike, whereNull, whereNotNull, whereFullText, compileIndexHint, compileGroupLimit,
useLegacyGroupLimit, compileLegacyGroupLimit, compileInsertOrIgnore, compileInsertOrIgnoreUsing,
compileJsonContains, compileJsonOverlaps, compileJsonContainsKey, compileJsonLength,
compileJsonValueCast, compileRandom, compileLock, compileInsert, compileUpdateColumns, compileUpsert,
compileJoinLateral, compileJsonUpdateColumn, compileUpdateWithoutJoins, prepareBindingsForUpdate,
compileDeleteWithoutJoins, compileThreadCount, wrapValue, wrapJsonSelector, wrapJsonBooleanSelector

**PostgresGrammar**
whereBasic, whereBitwise, whereLike, whereDate, whereTime, dateBasedWhere, whereFullText,
validFullTextLanguages, compileColumns, to, compileJsonContains, compileJsonContainsKey,
compileJsonLength, compileHaving, compileHavingBitwise, compileLock, compileInsertOrIgnore,
compileInsertOrIgnoreUsing, compileInsertGetId, compileUpdate, compileUpdateColumns, compileUpsert,
compileJoinLateral, compileJsonUpdateColumn, compileUpdateFrom, compileUpdateWheres,
compileUpdateJoinWheres, prepareBindingsForUpdateFrom, compileUpdateWithJoinsOrLimit,
prepareBindingsForUpdate, compileDelete, compileDeleteWithJoinsOrLimit, compileTruncate,
compileThreadCount, wrapJsonSelector, wrapJsonBooleanSelector, wrapJsonBooleanValue,
wrapJsonPathAttributes, parseJsonPathArrayKeys, substituteBindingsIntoRawSql, getOperators

PostgreSQL custom operators are supported. `CustomOperators` registers process-wide operators,
`GetOperators` includes them, and raw SQL substitution preserves escaped question-mark operators.
Registration and lookup are protected for concurrent use. The upstream `testCustomOperators`
case and Go concurrency regressions are in `grammars/laravel_port_test.go`.

**SQLiteGrammar**
compileLock, wrapUnion, whereBasic, whereLike, prepareWhereLikeBinding, whereDate, whereDay,
whereMonth, whereYear, whereTime, dateBasedWhere, compileIndexHint, compileJsonLength,
compileJsonContains, prepareBindingForJsonContains, compileJsonContainsKey, compileGroupLimit,
compileUpdate, compileInsertOrIgnore, compileInsertOrIgnoreUsing, compileUpdateColumns, compileUpsert,
groupJsonColumnsForUpdate, compileJsonPatch, compileUpdateWithJoinsOrLimit, prepareBindingsForUpdate,
compileDelete, compileDeleteWithJoinsOrLimit, compileTruncate, wrapJsonSelector

**SqlServerGrammar**
compileSelect, compileColumns, compileFrom, compileIndexHint, whereBitwise, whereDate, whereTime,
compileJsonContains, prepareBindingForJsonContains, compileJsonContainsKey, compileJsonLength,
compileJsonValueCast, compileHaving, compileHavingBitwise, compileDeleteWithoutJoins, compileRandom,
compileLimit, compileRowNumber, compileOffset, compileLock, wrapUnion, compileExists,
compileUpdateWithJoins, compileUpsert, prepareBindingsForUpdate, compileJoinLateral,
compileSavepoint, compileSavepointRollBack, compileThreadCount, getDateFormat, wrapValue,
wrapJsonSelector, wrapJsonBooleanValue, wrapTable, wrapTableValuedFunction

Dialect selection in this port is driven by `database.Config.Driver` (values `mysql`, `postgres`,
`sqlite`, `sqlserver`). The four external driver modules keep implementing the DDL grammar and are
not modified.

## 7. Public Builder and DB surface

The public contract is `contracts/database/db/db.go`. Read and write execution belongs to
`database/db`; it does not need duplicate execution methods on the SQL-only `query.Builder`.
The following groups have public entry points, compiler wiring and behavior tests:

| Group | Public API / behavior | Implementation and tests |
|---|---|---|
| Mutable fluent queries | Where/Select/From/Join/Order/Limit mutate and return the same object; independent branches use explicit `Clone` | `db/query.go`, `query_clauses.go`, `query_mutability_test.go` |
| Subqueries and joins | SelectSub, FromSub, AddSelect, WhereIn, WhereExists, JoinSub, lateral and cross joins, JoinClause | `query_clauses.go`, `join_clause.go`, `query_laravel_api_test.go`, `query_preview_test.go` |
| Predicates | OR/negated variants; case-sensitive LIKE; integer IN; column/value ranges; dates; row values; JSON overlaps; full text | `query.go`, `query_clauses.go`; existing upstream query corpus |
| Query state | Union/UnionAll, grouping/having, raw components, index hints, GroupLimit, ReorderDesc, random seed, page/keyset positioning, bindings/getters | `query_clauses.go`, `query/laravel_api.go` |
| Reads | First/Find, Sole/SoleValue, Value/RawValue, Pluck/Implode, Exists, aggregates and Explain | `query.go`, `query_results.go`, `aggregate.go` |
| Traversal | Chunk/ChunkById/ChunkByIdDesc, ChunkMap, EachById, Lazy/LazyById/LazyByIdDesc | `query.go`, `query_results.go`; lazy iteration stops without fetching another chunk |
| Writes | Insert, InsertGetID with optional key, InsertOrIgnore, InsertUsing, InsertOrIgnoreUsing, UpdateFrom, UpdateOrInsert callbacks, Upsert, IncrementEach/DecrementEach, Truncate | `query_writes.go`, `query.go`, `tests/db_laravel_builder_test.go` |
| SQL previews | Existing previews plus all new write methods; connection escaping, binary values, prepared booleans/dates and native placeholders | `to_sql.go`, `to_sql_writes.go`, `query_preview_test.go` |
| Query lifecycle | BeforeQuery is consumed at the upstream execution point; AfterQuery can transform results | `query_clauses.go`, `query_results.go`, `query_laravel_api_test.go` |
| Basic pagination | Paginate, SimplePaginate and existing CursorPaginate | `query.go`, `query_results.go`; no URL or presentation state |

Nested, subquery and BeforeQuery callbacks use the supplied builder and ignore replacement
return values, matching Laravel. When/Unless/Pipe retain their own return semantics.
Aggregate and pagination-count compilation propagate errors from their internal clones.
SUM of an empty set is zero; nullable AVG/MIN/MAX can be read through `sql.Null*` destinations.

`UpdateFrom` is PostgreSQL-specific. SQL Server refuses case-sensitive LIKE and insert-ignore;
MariaDB refuses lateral joins, including when reached through the MySQL driver. These failures
surface through the compiler before the requested statement executes.

## 8. Connections, processors, Schema and DDL

| Layer | Connected behavior |
|---|---|
| Connection | Named handles, purge/reconnect, writer metadata reads, sticky and explicit writer routing, query hooks/logs, duration thresholds, Pretend, server versions and escaping |
| Pool selection | Reader candidates are shuffled and tried until a connection opens; unusable empty reader entries retain the writer fallback. A configured set of unreachable readers returns its connection error |
| Recovery | `database/sql` manages physical connections. An ordinary read can retry once after connection loss, before returning rows; writes, pinned connections and transactions are not transparently replayed |
| Transactions | Nested savepoints on one connection; transaction levels; rollback to a level; retryable conflicts; ordered commit/rollback callbacks; no retry after a successful commit |
| Processor | Select, InsertGetID and normalized schemas/tables/views/types/columns/indexes/foreign keys; nullable metadata and generated expressions are preserved |
| Schema | Runtime metadata uses the new processor and writer; qualified table listings by default, explicit short-name mode, schema selectors and case-aware existence checks |
| DDL | Conditional schema operations, database creation/drop, table/column options, generated/computed columns, dialect types and indexes, temporary tables, constraint scopes and SQLite rebuilds |
| Cleanup | Pinned scopes close leases and roll back unfinished transactions; constraint restoration runs with a cleanup context even when the request is cancelled |

SQLite rebuilds copy data and preserve applicable indexes, foreign keys, checks and triggers.
Missing source columns fail explicitly. Identifiers are tokenized as UTF-8; quoted text and
comments are not renamed as columns. Dropping foreign keys by name is rejected as in Laravel;
use their local column list.

The external MySQL/PostgreSQL/SQLite/SQL Server drivers remain at v1.18.0. The core adapts their
existing DDL compilers and supplies the added metadata/DDL paths; no unshipped driver release is
required. The ORM receives only the shared connection compatibility changes in this scope.

## 9. Go adaptations and scope boundaries

| Laravel | Go adaptation |
|---|---|
| Optional parameters and overloads | Variadics or `any` at documented polymorphic entry points; old ordinary calls remain valid |
| Query results / Collection | Explicit `dest` arguments, slices/maps, channels and error-aware `RowIterator` |
| `paginate` | `Paginate(page, limit, dest, total *int64)`; defaults 1/15, supplied nonzero total skips COUNT, zero total skips the data query |
| `simplePaginate` | `SimplePaginate(page, limit, dest) (hasMore, error)`; limit+1 probe without COUNT, probe row removed |
| Cursor pagination | Existing data/cursor/has-more value, without total or HTTP metadata |
| Pagination display features | Excluded: URLs, paths, links, query parameters, fragments, rendering, HTTP lookup and the Paginator class hierarchy |
| SQL-only compiler | `To*SQL` returns SQL and bindings; facade previews use native database/sql placeholders; Truncate previews return a statement list |
| PDO connection handles | `database/sql` pools and explicit leases; reconnect returns the refreshed DB handle; existing transactions retain their leased connection |
| PDO InsertGetId | MySQL/SQLite use LastInsertId; PostgreSQL reads RETURNING; SQL Server reads OUTPUT inserted on the writer |
| SQL Server JSON updates | Existing JSON_MODIFY support is retained for framework compatibility |
| Introspection / debugging | Builder grammar/processor accessors, DB connection methods, ToSql/ToRawSql and query logs replace PHP runtime/debug helpers |
| Dynamic PHP extensions, Eloquent / model features | Outside this database port; no ORM feature work |

A mutable Builder is not safe for simultaneous mutation. Use `Clone()` for independent query
branches, and obtain request-local connection state with `WithContext`.

## 10. Verification and upstream mapping

See [LARAVEL_TESTS.md](LARAVEL_TESTS.md) for the 16 processor/query-grammar upstream methods and
[../LARAVEL_DB_TESTS.md](../LARAVEL_DB_TESTS.md) for DB, connection, transaction, Schema and DDL
mappings. Those 16 methods are a bounded corpus, not the entire database abstraction's coverage.

The root module tests do not require live databases. The separate `tests/` module exercises
real drivers. SQL Server requires an actual amd64 run; an ARM run that skips it is insufficient.
MariaDB can replace the test MySQL instance with `GORAVEL_TEST_MARIADB=1`.

```sh
env -u NO_COLOR go test ./... -timeout 90s
env -u NO_COLOR go test -race ./database/db ./database/driver ./database/schema ./database/query/...
cd tests
env -u NO_COLOR go test -v -count=1 -timeout 25m ./...
```
