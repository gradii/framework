# Laravel 数据库公开入口与测试映射

基线：Laravel `12.x`，`0fc645c673`；Goravel 移植前基线 `17ce480b`。
范围及分页约束见 [计划](LARAVEL_PORT_PLAN.md)，编译器规则见 [PARITY](query/PARITY.md)。
本文件区分公开执行、编译覆盖和依赖数据库能力的项目；不使用少量 Grammar 用例推算整个 DBAL 覆盖率。

## 查询入口与执行

| Laravel 来源与行为 | Go 公开入口 / 实现 | 行为测试 |
|---|---|---|
| `Query/Builder` fluent methods 返回 `$this`；`clone` 显式复制 | `db.Query`、`db/query.go`、`query_clauses.go` | `query_mutability_test.go`、`TestPublicLaravelClausesMutateAndExplicitCloneIsolates` |
| `whereIn`、`whereExists`；修改型回调忽略替换返回值 | WhereIn/NotIn/Exists 及 OR 变体、SelectSub/FromSub、JoinSub | `TestPublicQueryOptionalArguments`、`TestPublicBeforeQueryAndSubqueryCallbacks`、`tests/TestLaravelPublicParametersAndWritePreview` |
| `whereLike` 及 OR/NOT、`inRandomOrder`；`testWhereLikeClause*` | 可选 caseSensitive 和 seed 参数 | `query_preview_test.go`、上游 query corpus、四驱动公开执行测试 |
| `insertGetId` 的 sequence 参数；`testPreservedAreAppliedByInsertGetId` | `Query.InsertGetID` → 编译器 → Processor → writer | `query_test.go`、`tests/TestLaravelInsertGetIDCustomPrimaryKey` |
| `testInsertUsingMethod`、`test*InsertOrIgnore*`、`testUpsertMethod*`、`testUpdateFromMethodWithJoinsOnPostgres` | `query_writes.go`、`to_sql_writes.go`，SQL 预览与执行共用编译器 | `TestInsertUsingExecutesWriterAndCleansBindings`、`TestUpsertDefaultAndExplicitValues`、`TestPublicWriteSQLPreviews`、`tests/db_laravel_builder_test.go` |
| `testPreservedAreAppliedBy*`、beforeQuery/afterQuery | 修改型回调消费、结果转换、预览不执行写操作 | `query_laravel_api_test.go`、`TestPluckPreviewConsumesCallbackAndRestoresColumns`、`tests/TestLaravelPublicParametersAndWritePreview` |
| `testAggregateFunctions`、`testAggregateResetFollowedBy*` | Count/Sum/Avg/Min/Max；聚合克隆、SUM 空集为 0、nullable 目标 | `tests/TestLaravelAggregatesPreserveNullAndReturnZeroSum`、已有聚合及绑定回归 |
| `onceWithColumns`、`first`、`sole`、`exists` | 临时列恢复、LIMIT 1/2、编译 EXISTS | `query.go`、`query_results.go`、`query_laravel_api_test.go`、DB 集成用例 |
| `Concerns/BuildsQueries` 的 chunk/lazy/keyset | Chunk/ChunkMap/EachById/Lazy 系列，保留排序、offset/limit、提前终止 | `TestLazyStopsBeforeFetchingTheNextChunk`、`tests/TestLaravelPublicBuilderWritesAndPagination`、已有 chunk/cursor 用例 |
| `paginate` / `simplePaginate` 的查询部分 | Paginate、SimplePaginate；预提供 total、limit+1、溢出检查、无 COUNT 的简单分页 | `TestSimplePaginate*`、`query_preview_test.go`、DB 分页集成用例 |
| `Connection::prepareBindings` / escape / `toRawSql` | `driver.Escape`、`db/expression.go`、`ToRawSql`；二进制、布尔值、日期、问号运算符 | `TestRawSQLUsesConnectionEscapingAndPreparedBindings`、`interpolate_test.go`、Grammar 上游转义用例 |

## 连接与事务

| Laravel 来源 | Go 接线 | 测试与边界 |
|---|---|---|
| `Connection` 查询事件、beforeExecuting、累计耗时、pretend、sticky | `db/session.go`、`connection_api.go`；请求状态由 WithContext 隔离 | `session_test.go`，真实 DB query log / 读写分离回归 |
| `Connectors/ConnectionFactory` 候选主机、连接丢失检测 | `driver/connection.go`、`errors.go`；随机候选与连接失败；database/sql 管理物理连接 | `connection_test.go`、`TestLostConnectionReadRetriesWithoutRepeatingHooks` |
| 写操作结果不确定时禁止重复执行；活动事务固定连接 | 显式执行类型，事务/固定连接/写入不透明重放 | `TestLostConnectionDoesNotReplayWritesOrTransactionalReads` |
| `Concerns/ManagesTransactions`、`DatabaseTransactionsManager` | `db/transaction.go`；savepoint、层级、指定回滚、重试、回调归属 | `transaction_test.go`、`tests/TestLaravelNestedTransactionsAndCallbacks` |
| `DatabaseTransactionsTest.php`、`DatabaseTransactionsManagerTest.php` 的提交与回滚排序 | 已提交子层回调先于父层；父回滚清理子层；提交后错误不触发重试 | `TestTransactionCommitCallbacksFollowLaravelStagingOrder`、`TestTransactionRollbackRemovesCommittedDescendants`、`TestTransactionDoesNotRetryAnAfterCommitFailure` |
| PDO 与 Go 连接模型的适配；BEGIN 前解析版本，避免单连接池借用第二条连接 | AcquireWriteConnection/WithWriteConnection、StatementContext；关闭作用域回滚未完成事务 | `write_connection.go`、`TestLaravelSingleConnectionTransactionCompilesVersionedQueries`、临时表及取消后恢复测试 |

## Processor、Schema 与 DDL

| Laravel 来源 | 公开能力与实现 | 测试 |
|---|---|---|
| `Query/Processors/*Processor` | Select/InsertGetID/Schemas/Tables/Views/Types/Columns/Indexes/ForeignKeys | [16 个原始方法映射](query/LARAVEL_TESTS.md)、`processors/metadata_test.go`、`columns_test.go` |
| `Schema/Builder`、`SchemaBuilderSchemaNameTest.php` | GetSchemas/GetTables/GetViews/GetTypes、默认限定名称、schema 选择器、前缀和存在性检查 | `schema/metadata*.go`、`tests/schema_laravel_metadata_test.go`、原有 Schema 全量用例 |
| `SqlServerProcessor::processColumns` | 与上游一样使用 `precision` 展示 float 和时间类型，不改成 `places` | `TestSqlServerColumnMetadata`、amd64 `TestColumnTypes_Sqlserver` |
| `Schema/Blueprint`、各方言 SchemaGrammar | table/column options、原生类型、generated/computed、数据库与条件修改入口 | `schema/ddl.go`、`blueprint_options.go`、`column_options.go`、`tests/TestLaravelAdvancedDDL` |
| `SQLiteGrammar::compileAlter`、`compileDropForeign` | 重建、搬迁数据、保留约束/索引/触发器、明确拒绝按名称删外键 | `tests/TestSQLiteRebuildPreservesDataConstraintsAndConnectionState`、`TestSQLiteRebuildUnicodeAndMissingColumns` |
| MariaDB 元数据和扩展类型 | 保留显示宽度、默认值及外键规则；SRID 使用 ref_system_id，原生 vector | `TestColumnTypes_Mysql` 的 MariaDB 期望、`TestLaravelMariaDBSpatialAndVector` |
| 外键开关与恢复 | 原设置恢复，嵌套作用域共用固定连接，请求取消后仍执行清理 | `tests/TestSQLiteConstraintScopeRestoresAfterCancellation` |
| 临时表的连接归属 | Schema.WithWriteConnection → 同一物理 writer → Create/Insert/Select/Drop | `tests/TestLaravelScopedTemporaryTable` |

方言能力边界：

| 能力 | MySQL / MariaDB | PostgreSQL | SQLite | SQL Server |
|---|---|---|---|---|
| UpdateFrom | 拒绝 | 支持 | 拒绝 | 拒绝 |
| InsertOrIgnore / Using | 支持 | 支持 | 支持 | 拒绝 |
| caseSensitive LIKE | 支持 | 支持 | GLOB 转换 | 拒绝 |
| lateral join | MySQL 支持；MariaDB 拒绝 | 支持 | 拒绝 | APPLY |
| Generated 列 | VirtualAs / StoredAs | 依服务器支持与上游 SQL | VirtualAs / StoredAs、重建 | Computed / Persisted |
| Spatial 列 / 索引 | 原生类型 / spatial index | PostGIS 类型 / GiST | geometry 声明；索引拒绝 | geometry/geography / spatial index |
| Vector | 服务器版本支持时使用原生 vector | 需要 pgvector | 拒绝 | 拒绝 |

PostGIS、pgvector 及数据库服务器的可选特性必须由部署环境提供；本移植不安装数据库扩展。
没有运行可选扩展的实例时，不能把对应 SQL 编译覆盖写成真实执行验证。

## 验证记录

验证日期：2026-10-24。最新结果：

| 检查 | 结果 |
|---|---|
| 根模块 Go 1.27 | 全量通过；最后的 DB 修改另经相关包/race 验证 |
| 根模块 Go 1.26.2 | 全量通过；最后的 DB 修改另经该版本 DB 包测试 |
| database/db、driver、schema、query 的 race | 通过 |
| PostgreSQL / SQLite / MariaDB 11.8 完整 tests 模块 | 通过；MariaDB spatial/vector 已实际执行 |
| 单连接事务、锁竞争最终 MariaDB 回归 | 通过 |
| Linux amd64：MySQL / PostgreSQL / SQLite / SQL Server 2022 | 串行全量通过，测试进程退出码 0 |
| 增量 golangci-lint（对比 17ce480b） | 0 issues |

全量 lint 另有基线遗留问题，未将 ORM 等范围外的旧问题并入本次功能修改。
PostGIS/pgvector 的外部扩展实例未单独启动，此项仅声明实现和运行环境依赖，不列为实库验证。
以下命令保留复现方式。

```sh
env -u NO_COLOR go test ./... -timeout 90s
env -u NO_COLOR GOTOOLCHAIN=go1.26.2 go test ./... -timeout 90s
env -u NO_COLOR go test -race ./database/db ./database/driver ./database/schema ./database/query/... -timeout 3m
cd tests
env -u NO_COLOR go test -v -count=1 -timeout 25m ./...
env -u NO_COLOR GORAVEL_TEST_MARIADB=1 go test -v -count=1 -timeout 25m ./...
```

SQL Server 完整集成须在 amd64 执行。macOS ARM 可交叉编译 Linux amd64 测试二进制，
在挂载 Docker socket 的容器中运行；容器需时区数据（可通过 ZONEINFO 指向 Go 的 zoneinfo.zip）。
持久化容器的 /tmp 测试缓存以复用数据库；本地资源有限时使用 -test.parallel=1，SQL Server 测试实例限制为 2048 MB。
MariaDB 测试使用独立的容器缓存键，且用 ServerVersion 断言验证实际后端。
环境的 NO_COLOR 会影响已有终端颜色快照，应在测试命令中移除。
