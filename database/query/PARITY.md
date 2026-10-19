# Laravel Query Builder parity checklist

**Purpose.** This file is the coverage proof for porting `Illuminate\Database`'s query builder into
this package. Every item is either implemented or recorded as a deliberate Go deviation. Review
this file first when judging the fidelity of the port.

**Source of truth** (Laravel checkout at `/Users/dog/OpenSource/framework`):

| File | Size |
|---|---|
| `src/Illuminate/Database/Query/Builder.php` | 240 functions |
| `src/Illuminate/Database/Query/JoinClause.php` | extends Builder |
| `src/Illuminate/Database/Grammar.php` | 20 functions (base) |
| `src/Illuminate/Database/Query/Grammars/Grammar.php` | 98 functions |
| `src/Illuminate/Database/Query/Grammars/MySqlGrammar.php` | 29 functions |
| `src/Illuminate/Database/Query/Grammars/PostgresGrammar.php` | 40 functions |
| `src/Illuminate/Database/Query/Grammars/SQLiteGrammar.php` | 30 functions |
| `src/Illuminate/Database/Query/Grammars/SqlServerGrammar.php` | 35 functions |
| `src/Illuminate/Database/Query/Processors/Processor.php` | 9 functions |

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

**MySqlGrammar (29)**
whereLike, whereNull, whereNotNull, whereFullText, compileIndexHint, compileGroupLimit,
useLegacyGroupLimit, compileLegacyGroupLimit, compileInsertOrIgnore, compileInsertOrIgnoreUsing,
compileJsonContains, compileJsonOverlaps, compileJsonContainsKey, compileJsonLength,
compileJsonValueCast, compileRandom, compileLock, compileInsert, compileUpdateColumns, compileUpsert,
compileJoinLateral, compileJsonUpdateColumn, compileUpdateWithoutJoins, prepareBindingsForUpdate,
compileDeleteWithoutJoins, compileThreadCount, wrapValue, wrapJsonSelector, wrapJsonBooleanSelector

**PostgresGrammar (40)**
whereBasic, whereBitwise, whereLike, whereDate, whereTime, dateBasedWhere, whereFullText,
validFullTextLanguages, compileColumns, to, compileJsonContains, compileJsonContainsKey,
compileJsonLength, compileHaving, compileHavingBitwise, compileLock, compileInsertOrIgnore,
compileInsertOrIgnoreUsing, compileInsertGetId, compileUpdate, compileUpdateColumns, compileUpsert,
compileJoinLateral, compileJsonUpdateColumn, compileUpdateFrom, compileUpdateWheres,
compileUpdateJoinWheres, prepareBindingsForUpdateFrom, compileUpdateWithJoinsOrLimit,
prepareBindingsForUpdate, compileDelete, compileDeleteWithJoinsOrLimit, compileTruncate,
compileThreadCount, wrapJsonSelector, wrapJsonBooleanSelector, wrapJsonBooleanValue,
wrapJsonPathAttributes, parseJsonPathArrayKeys, substituteBindingsIntoRawSql, getOperators

> **Deliberate non-support: the custom-operator registry.** Laravel's `Grammar::getOperators()` merges a
> caller-registered list into the dialect's own operators, and `PostgresGrammar::customOperators()` is the
> static setter for it. This port keeps no such registry: `PostgresGrammar.GetOperators` returns the dialect
> list and nothing more, and the corresponding corpus methods
> (`DatabasePostgresQueryGrammarTest::testCustomOperators`) are therefore recorded as a documented
> non-support rather than an unported gap. A caller that needs a custom operator should express it as a raw
> clause. Adding the registry is a deliberate future decision, not a missing port.


**SQLiteGrammar (30)**
compileLock, wrapUnion, whereBasic, whereLike, prepareWhereLikeBinding, whereDate, whereDay,
whereMonth, whereYear, whereTime, dateBasedWhere, compileIndexHint, compileJsonLength,
compileJsonContains, prepareBindingForJsonContains, compileJsonContainsKey, compileGroupLimit,
compileUpdate, compileInsertOrIgnore, compileInsertOrIgnoreUsing, compileUpdateColumns, compileUpsert,
groupJsonColumnsForUpdate, compileJsonPatch, compileUpdateWithJoinsOrLimit, prepareBindingsForUpdate,
compileDelete, compileDeleteWithJoinsOrLimit, compileTruncate, wrapJsonSelector

**SqlServerGrammar (35)**
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

## 7. Builder surface

Grouped checklist. Boxes are ticked as the port lands.

**Construction and state**
- [ ] `select` `selectSub` `selectRaw` `addSelect` `distinct`
- [ ] `from` `fromSub` `fromRaw`
- [ ] `useIndex` `forceIndex` `ignoreIndex`
- [ ] `newQuery` `clone` `cloneWithout` `cloneWithoutBindings`
- [ ] `getColumns` `getLimit` `getOffset` `getBindings` `getRawBindings` `setBindings`
- [x] `getConnection` `getProcessor` `getGrammar` `useWritePdo` `raw`

**Joins**
- [x] `join` `joinWhere` `joinSub` `joinLateral`
- [ ] `leftJoin` `leftJoinWhere` `leftJoinSub`
- [ ] `rightJoin` `rightJoinWhere` `rightJoinSub`
- [x] `crossJoin` `crossJoinSub`
- [ ] JoinClause `on` `orOn`

**Where**
- [x] `where` `orWhere` `whereNot` `orWhereNot` `prepareValueAndOperator` `mergeWheres`
- [ ] `whereColumn` `orWhereColumn` `whereRaw` `orWhereRaw`
- [ ] `whereLike` `orWhereLike` `whereNotLike` `orWhereNotLike`
- [ ] `whereIn` `orWhereIn` `whereNotIn` `orWhereNotIn`
- [ ] `whereIntegerInRaw` `orWhereIntegerInRaw` `whereIntegerNotInRaw` `orWhereIntegerNotInRaw`
- [ ] `whereNull` `orWhereNull` `whereNotNull` `orWhereNotNull`
- [ ] `whereBetween` `whereBetweenColumns` `orWhereBetween` `orWhereBetweenColumns`
- [ ] `whereNotBetween` `whereNotBetweenColumns` `orWhereNotBetween` `orWhereNotBetweenColumns`
- [ ] `whereValueBetween` `orWhereValueBetween` `whereValueNotBetween` `orWhereValueNotBetween`
- [ ] `whereDate` `whereTime` `whereDay` `whereMonth` `whereYear` and `or` variants
- [ ] `whereNested` `forNestedWhere` `addNestedWhereQuery`
- [ ] `whereExists` `orWhereExists` `whereNotExists` `orWhereNotExists` `addWhereExistsQuery`
- [ ] `whereRowValues` `orWhereRowValues`
- [ ] `whereJsonContains` `whereJsonDoesntContain` `whereJsonOverlaps` `whereJsonDoesntOverlap`
- [ ] `whereJsonContainsKey` `whereJsonDoesntContainKey` `whereJsonLength` and `or` variants
- [ ] `whereFullText` `orWhereFullText`
- [ ] `whereAll` `whereAny` `whereNone` and `or` variants, `dynamicWhere`

**Grouping, having, order, limit, union, lock**
- [ ] `groupBy` `groupByRaw` `having` `orHaving` `havingNested` `addNestedHavingQuery`
- [ ] `havingNull` `havingNotNull` `havingBetween` `havingRaw` `orHavingRaw`
- [ ] `orderBy` `orderByDesc` `latest` `oldest` `inRandomOrder` `orderByRaw`
- [ ] `reorder` `reorderDesc` `skip` `offset` `take` `limit` `groupLimit`
- [ ] `forPage` `forPageBeforeId` `forPageAfterId`
- [ ] `union` `unionAll` `lock` `lockForUpdate` `sharedLock`

**Reads and aggregates**
- [ ] `toSql` `toRawSql` `find` `findOr` `value` `soleValue` `get`
- [x] `rawValue`
- [x] `cursor` `pluck` `implode` `exists` `doesntExist` `existsOr` `doesntExistOr`
- [ ] `count` `min` `max` `sum` `avg` `aggregate` `numericAggregate`
- [x] `paginate` `simplePaginate` `cursorPaginate` `getCountForPagination`
- [x] `beforeQuery` `applyBeforeQueryCallbacks`
- [ ] `afterQuery` `applyAfterQueryCallbacks`

**Writes**
- [ ] `insert` `insertOrIgnore` `insertGetId` `insertUsing` `insertOrIgnoreUsing`
- [ ] `update` `updateFrom` `updateOrInsert` `upsert`
- [ ] `increment` `incrementEach` `decrement` `decrementEach` `delete` `truncate`

**Traits mixed into the builder (`Concerns/BuildsQueries`)**
- [ ] `chunk` `chunkMap` `each` `chunkById` `chunkByIdDesc` `eachById`
- [ ] `lazy` `lazyById` `lazyByIdDesc` `first` `firstOrFail` `sole` `tap` `pipe`

## 8. Deliberate Go deviations

These are intentional and must be documented in code, not treated as gaps.

| Laravel | Go in this port | Reason |
|---|---|---|
| `Closure` callbacks | `func(Query) Query` | Go has no closure-to-builder idiom; matches existing Goravel style |
| `paginate` returns a `LengthAwarePaginator` | returns a paginator value; no `total *int64` out-param | Go has no out-params; the current signature is `Paginate(page, limit int, dest any, total *int64) error` |
| `cursor()` returns a `LazyCollection` | returns a channel or iterator | matches existing `Cursor() chan db.Row` |
| Relations declared as methods (`hasMany(...)`) | declared in `Relations() map[string]Relation` with `gorm:"-"` fields | gorm struct tags remain the mapping mechanism by choice |
| Model attribute pipeline (casts, accessors, mutators, appends, hidden/visible, `toArray`/`toJson`) | out of scope | scope decision; the existing model layer stays |
| `Collection` type | out of scope | scope decision |
| Laravel-style `Relation` objects | existing descriptor system stays | scope decision |
| `$component` presence via `isset` | explicit nil or presence flag | Go fields always exist |

## 9. Verification notes

- **Runs without a database:** `go test ./database/db/... ./database/gorm/...` passes today. Both
  packages assert SQL through mocks and a stub gorm dialector. `database/gorm/relation_test.go`
  defines `stubDialector` (a no-op dialector plus `RegisterDefaultCallbacks`) which is the pattern to
  reuse for grammar tests.
- **Needs Docker:** the `tests/` module boots real databases. `tests/query.go` calls
  `sqlite.NewDocker(...)` and friends, so the exact-string assertions in `tests/to_sql_test.go`
  cannot run until a Docker daemon is available.
- **Golden strings to preserve or change deliberately**, for example from `tests/to_sql_test.go`:
  `SELECT count(*) FROM "users" WHERE "id" = $1 AND "users"."deleted_at" IS NULL`.
- **Coverage proof:** every row above is ticked against the Laravel file named in the header table.
