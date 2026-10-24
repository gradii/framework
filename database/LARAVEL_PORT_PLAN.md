# Laravel Query Builder 与数据库抽象层移植计划

状态：本计划范围内的移植与验收已完成（2026-10-24）。以 Laravel 12.x 的实际行为和上游用例为准；分页边界保持第 2 节约定。

当前进度（实施记录）：

- P1 已实现公开查询/写入、SQL 预览、基础分页、Lazy、回调和原地修改语义，并补齐子查询、LIKE、随机种子、InsertGetID 参数及空集聚合。
- P2 已接入会话状态、查询事件、pretend、sticky、连接管理、服务器版本和转义；reader 候选失败处理、读操作恢复及取消后的连接清理已补充回归。
- P4 已实现基于事务句柄的 savepoint、回调记录和事务重试；已验证子事务回调排序、回滚清理及提交后错误不重试。
- P3 元数据处理链已接通；按上游保留 schema 范围、限定名称和 SQL Server precision 展示语义。
- P5 的 DDL、生成列、临时表、SQLite 重建和外键约束作用域已实现；已验证 Unicode 列、缺失列、数据/约束保留及取消恢复。
- P6 已完成：Go 1.26/1.27、数据库 race、Linux amd64 四驱动全量集成、MariaDB 11.8 全量集成及增量 lint 均通过。可选扩展实例的验证边界见 [测试映射](LARAVEL_DB_TESTS.md)。
- 实现、自动生成 mocks、测试和文档一并提交。分页不包含 URL 的约束继续适用于所有后续改动。

基线：Goravel framework `17ce480b`；本地 Laravel `12.x`，提交 `0fc645c673`。
上游参考目录：`~/OpenSource/framework/src/Illuminate/Database`。

## 1. 目标与范围

完成 DB 查询入口、Builder 剩余 API、Processor/Schema、连接层、事务语义和 DDL 的移植与接通。
验收关注公开 API 的实际执行行为、SQL 和绑定，以及真实数据库结果。

本计划中的 DBAL 指 Laravel 的连接、执行、事务和 Schema 抽象。
这份 Laravel 12.x 源码没有 `doctrine/dbal` 依赖，不引入 Doctrine DBAL 或模拟 PHP PDO 对象。

ORM 功能不在本次移植范围。共享连接、驱动合约发生变化时，需要保留现有 ORM 适配和回归兼容。
已有能力优先复用；命名差异、Go 的目标参数和迭代器等实现方式不单独视为未移植功能。

## 2. 分页范围：已确定

保留现有 `Paginate`，新增轻量 `SimplePaginate`。已有 `CursorPaginate` 保留兼容和回归覆盖，
本轮不扩展为完整 Laravel Pagination 子系统。

| API | 数据与状态 | 数据库行为 |
|---|---|---|
| `Paginate(page, limit int, dest any, total *int64) error` | 当前页数据通过 `dest` 返回，总条数通过 `total` 返回 | 保留现有总数解析规则，再执行 offset/limit 查询 |
| `SimplePaginate(page, limit int, dest any) (hasMore bool, err error)` | 当前页数据通过 `dest` 返回，返回值表示是否还有下一页 | 查询 `limit + 1` 条，裁掉探测行；不执行 COUNT |
| 现有 `CursorPaginate` | 当前页数据、页大小、游标和是否还有后续数据 | 保留现有 keyset 查询与游标行为 |

分页约束：

- 分页参数由调用方显式传入。分页代码不读取 HTTP 请求、路由或当前站点配置。
- 分页结果、状态对象及其序列化内容均不包含 URL、path、链接、查询参数或 fragment。
  不提供链接生成、`appends`、`withQueryString`、HTML 渲染、视图主题或分页窗口功能。
- `Paginate` 保持现有签名和默认值：`page <= 0` 使用 1，`limit <= 0` 使用 15。
  调用方传入非零总数时跳过 COUNT；总数未提供或为零时统计；解析后的总数为零时跳过数据查询。
- `SimplePaginate` 使用相同的页码和页大小约定，只增加查询层的 `hasMore` 能力，不引入 Paginator 类体系。
- 页码、页大小和总条数足以由调用方计算总页数；不为基础分页增加额外框架层。
- 回归覆盖空页、末页、恰好一页、预提供总数、distinct/group/having/union 计数、绑定顺序和整型溢出边界。
  明确验证 SimplePaginate 的 SQL 调用中没有 COUNT，返回数据不包含探测行。

## 3. 架构与兼容原则

- `contracts/` 定义公开接口和共享数据结构；实现包不能反向成为 contracts 的依赖。
- `database/query` 管理查询状态与组合；Query Grammar 编译 SQL；`database/db` 对接公开 DB API 和执行。
- Connection 管理连接池、会话状态和执行策略；Processor 负责结果归一化；Schema/Blueprint 组织元数据查询和 DDL。
- 执行方式由明确的操作类型、驱动能力和 Processor 决定。保留当前 InsertGetID 的 Processor 分流设计。
- Processor 工厂及共享驱动标识放在不依赖具体 Builder、Schema 实现的层，避免接通 Schema 时产生循环导入。
- 连接池可以共享；事务、sticky、pretend、累计耗时及回调状态归属具体 DB 会话或请求上下文。
  同一会话中的查询共享状态，不同请求和连接名称之间保持隔离。
- 保持现有 API 调用兼容。新增查询入口优先增加方法，避免通过修改 `Select(...string)` 等签名破坏现有调用。
- DB 查询链遵循 Laravel 的可变 Builder：Where、Select、Join、Order 等修改当前对象并返回同一对象。
  clone 仅用于显式 Clone、上游明确隔离的聚合/分页计数/keyset 分批等路径；不沿用 ORM/GORM 的逐步克隆行为。
  嵌套查询与子查询使用上游的 newQuery；修改型回调的返回值不替换被传入的 Builder。
- 元数据先定义完整的规范化结构，再为现有 `driver.Column/Table/View` 等类型提供明确的兼容投影。
  不直接把现有字符串字段改为 `any`，也不在投影前丢弃 NULL、生成列表达式等信息。
- DDL 和旧 Processor 目前涉及 `goravel/mysql`、`postgres`、`sqlite`、`sqlserver` 外部模块。
  每阶段检查现有 v1.18.0 驱动的兼容性；需要驱动扩展的能力必须包含配套驱动改动和版本接入，不能只完成核心接口。

## 4. 实施阶段与依赖

| 阶段 | 交付内容 | 前置依赖 |
|---|---|---|
| P0 | 差异矩阵、接口和兼容设计 | 当前源码盘点 |
| P1 | DB 查询入口、Builder API、基础分页 | P0 |
| P2 | Connection 执行与会话能力 | P0、P1 的执行接口 |
| P3 | Processor 和 Schema 元数据完整接通 | P2 |
| P4 | 事务状态机、重试和回调 | P2 |
| P5 | Schema/Blueprint/DDL 补全 | P3、P4 |
| P6 | 跨驱动验收、文档和兼容收尾 | P1–P5 |

每阶段拆分为可独立验证的提交，测试随功能交付，不集中留到 P6。

### P0：固定基线与公开合约

1. 校正 [query/PARITY.md](query/PARITY.md)，修正 CustomOperators、InsertGetID 等过时条目。
2. 为范围内上游方法建立映射：上游方法及测试、Go 编译实现、公开 DB/Schema 入口、执行路径、当前状态。
   状态区分“已接通”“仅有底层实现”“待实现”“Go 等价实现”“本次范围外”。
3. 定稿 DB/JoinClause、连接会话、事务回调、规范化元数据与旧驱动适配合约。
   分页采用第 2 节约定；Getter、Avg 等已有等价能力复用现有实现。
4. 标记每项能力需要修改的核心包及外部驱动，确定新旧接口共存和驱动升级顺序。

验收：计划内每项能力都有明确入口、责任层、测试来源和依赖；不存在循环导入或未说明的接口破坏。

### P1：DB 查询入口与 Builder 剩余 API

按以下批次完成公开合约、Builder 接线、执行和 SQL 预览：

| 批次 | 工作内容 |
|---|---|
| 写入 | `InsertOrIgnore`、`InsertUsing`、`InsertOrIgnoreUsing`、`UpdateFrom`、`Upsert`、`Truncate`、`IncrementEach/DecrementEach`；保留 InsertGetID 并核对 sequence/主键列参数传递 |
| 查询组合 | Select/From 子查询及 raw 入口、AddSelect、索引提示、JoinSub/JoinLateral 及左右连接变体、Union/UnionAll、GroupLimit、重排和分页定位辅助方法 |
| 条件与聚合条件 | 整数 IN、日期/时间及相对日期方法、列/值区间、行值比较、JSON overlaps、FullText、嵌套条件，以及缺少的 OR/否定/Having 便利入口 |
| 执行生命周期 | 公开 BeforeQuery；补 AfterQuery/ApplyAfterQueryCallbacks；克隆和回调消费行为按对应上游执行路径验证 |
| 读取与遍历 | Sole/SoleValue、ChunkMap、EachById、Lazy/LazyById/LazyByIdDesc、必要的聚合结果处理、实际执行查询计划的 Explain |
| 基础分页 | 按第 2 节保留 Paginate，增加只返回 hasMore 的 SimplePaginate；回归现有 CursorPaginate |

实现要求：

- 复用已有编译器，避免在 DB 适配层重新拼装方言 SQL。
- 对不支持的组合明确返回框架错误，例如仅 PostgreSQL 支持的 UpdateFrom、MariaDB 不支持的 lateral join。
- 读取保持 `dest` 风格；Lazy 系列采用支持错误传播和提前终止的 Go 迭代方式，不引入 PHP Collection 系统。
- Explain 执行查询计划命令，与现有用于插值显示 SQL 的 `Builder.Explain` 区分。
- 新操作的 ToSql/ToRawSql 预览与实际执行共享编译来源；编译错误在执行前返回。

验收：新增能力可以经 `facades.DB().Table(...)` 使用；SQL、绑定、影响行数、返回数据、错误和回调时机有对应测试。
仅增加底层方法或仅通过 SQL 字符串断言不算完成公开执行入口。

### P2：Connection 执行与会话能力

1. 将查询、写入、返回行的写操作和元数据读取接到明确的执行入口；保留 ctx、trace、日志和取消传播。
   提供标量、单行、多行及多结果集等所需能力，元数据查询能够显式使用 writer。
2. 完善按名称获取、purge/reconnect 和配置刷新；定义缓存及连接池的所有权和释放行为。
   活跃事务保持其连接归属，刷新连接不改变已经开始的事务。
3. 接入 sticky 写后读、手动 writer 选择、锁查询和事务内 writer 路由，并验证会话隔离。
   核对读写配置继承、多候选端点选择及失败回退行为。
4. 增加 beforeExecuting、查询监听、累计查询耗时回调和 pretend 查询收集。
   Pretend 状态限定在调用作用域内，并保证该作用域不执行真实 SQL。
5. 建立驱动错误分类：区分取消、连接丢失、死锁和序列化冲突。
   结合 `database/sql` 已有恢复机制，避免对结果不确定的写操作进行透明重复执行。
6. 接入服务器版本/能力信息、MySQL legacy GroupLimit、`use_upsert_alias` 和连接 escaper。
   统一实际 DB SQL 预览与新 Grammar 的转义路径，覆盖二进制值、引号和问号运算符。

验收：读写归属、会话状态、连接刷新、pretend、hook 次数和错误传播可观测且有测试；不同请求的状态互不污染。

### P3：Processor 与 Schema 元数据

1. 补齐新 Processor 的 Select、InsertGetID、Schemas、Tables、Views、Types、Columns、Indexes、ForeignKeys 处理能力。
   现有旧驱动的索引、外键和类型处理按能力复用或适配，避免重复维护两套相同规则。
2. 接通 Schema 的真实调用路径，使公开元数据 API 使用新的规范化处理链。
   特别核对 GetColumns，不能继续只测试新的 ProcessColumns，而运行时仍完全使用旧处理器。
3. 保留并暴露生成列的 stored/virtual/computed 信息和表达式、schema-qualified name、NULL 与空值差异、索引列顺序、外键规则。
   覆盖 MySQL/MariaDB、PostgreSQL、SQLite 和 SQL Server 的返回格式差异。
4. 补充 GetSchemas、GetColumnType、多 schema 的表/视图/类型枚举和名称解析。
   核对 HasTable/HasColumn/HasIndex 的大小写、前缀、复合列及索引类型语义。

验收：从公开 Schema API 查询真实数据库，得到与上游约定一致的规范化信息；旧接口的兼容投影也有回归测试。

### P4：事务语义

1. 实现与具体事务句柄绑定的事务层级状态；嵌套事务使用同一数据库连接上的 savepoint。
   提供事务层级查询、指定层级回滚及各方言对应的 savepoint 能力。
2. 外层成功提交后执行提交回调；内层回滚只清理其层级应清理的回调和状态。
   完成 AfterCommit/AfterRollBack 的注册、嵌套传播、执行顺序和清理规则。
3. Transaction 默认执行一次，调用方可指定重试次数。
   仅对符合驱动错误分类的可重试冲突重跑整个顶层事务；嵌套冲突按上游规则向外传播。
4. 处理 begin/commit/rollback 失败、panic、ctx 取消和重试耗尽。
   事务已经提交后的回调错误不能触发数据库事务重试，防止业务重复执行。

验收：嵌套提交/回滚、回调归属、同连接约束、冲突重试及所有退出路径均有状态和结果断言；错误之后无事务状态残留。

### P5：Schema / Blueprint / DDL

按依赖和驱动能力分批交付：

1. Schema 管理入口：CreateDatabase/DropDatabaseIfExists、条件修改表/列/索引、外键约束开关及作用域恢复。
2. Blueprint 的表、列、索引选项：临时表、engine/charset/collation、raw 类型与缺少的专用便利入口。
   先核对现有通用 Column 能力，避免把缺少同名方法误判为缺少底层类型支持。
3. 生成列和相关修饰器：StoredAs、VirtualAs、Computed/Persisted、Invisible 等，连同元数据读取一起验收。
4. 方言扩展：空间类型/索引及范围矩阵内其他未覆盖类型，逐项声明支持的数据库和版本；不支持时返回明确错误。
5. ALTER 与状态管理：列修改、重命名、删除、索引及外键保留；SQLite 涉及重建表时验证数据搬迁和结构保留。
6. 按数据库能力使用 Schema 事务，定义多条 DDL 的执行顺序和失败后的状态，接入相应外部驱动编译实现。

验收：Create → 元数据读取 → Alter → 再读取 → Drop 的真实数据库闭环通过；原有数据、约束和索引的保留符合预期。

### P6：验收与文档收尾

- 每个功能从上游用例提取可观察行为，检查 SQL、绑定、结果、错误、回调和资源释放，而非只对齐方法名。
- 更新 [query/PARITY.md](query/PARITY.md) 及 [query/LARAVEL_TESTS.md](query/LARAVEL_TESTS.md)，为连接、事务、Schema/DDL 补充各自的测试映射。
  明确现有“16 个 Processor/Grammar 上游方法”不代表整个数据库抽象层的覆盖率。
- 保留已有 CursorPaginate、InsertGetID、PostgreSQL 自定义运算符及 ORM 共用连接适配的回归。
- 每项只有在公开入口、运行时接线、相关驱动实现、行为测试和文档均完成后，才能标记为完成。

## 5. 验证与提交要求

- 接口变更通过 `go tool mockery` 更新 mocks，不手工编辑 `mocks/`。
- 新错误统一在 `errors/list.go` 用框架 `New(...)` 声明；按标准 Go 命名和格式，使用 `any`。
- 对每批实现运行相关包测试；会话、事务、回调和缓存改动增加有意义的 race 测试。
- 阶段性运行 `go test ./database/...`；集成测试在 `tests` 模块运行 `go test -v -count=1 -timeout 25m ./...`。
- 覆盖 MySQL、PostgreSQL、SQLite、SQL Server；MariaDB 特有行为使用对应实例或明确的驱动兼容测试验证。
  SQL Server 完整集成验证在 amd64 环境进行，不能用 ARM 跳过后的通过代替。
- 覆盖项目要求的 Go 1.26/1.27 和现有 lint/CI 检查。外部驱动变更同时验证旧版本适配与新版本接入。
- 按功能批次提交，每批注明公开入口、方言支持和验证结果；不把分页 URL 或展示层功能并入任一批次。

## 6. 总体完成标准

计划内能力均可通过公开 DB/Schema API 使用，并有与目标方言一致的执行和元数据行为。
基础分页满足第 2 节约定，所有分页结果保持数据和状态层职责。
接口兼容、外部驱动接线、异常退出和并发状态隔离均完成验证；不存在仅新增了编译器或孤立测试而未接通运行时的条目。
