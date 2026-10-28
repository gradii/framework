# 数据库重构深度审查

日期：2026-10-28。审查 framework 与 MySQL/PostgreSQL/SQLite/SQL Server 工作区，包括当时暂存与未暂存改动。复现使用临时程序和隔离数据库对象。审查阶段只新增时间戳测试与此报告，没有修改运行时代码；随后按用户要求将当前结果保存为提交，保留以下未修问题和失败测试。

下文区分本轮新增行为、已有问题，以及原修复尚未覆盖的边界。之前单独记录的并发嵌套事务、旧 child 回滚和 ORM 无 savepoint 问题不重复计数。

## 优先处理的问题

### 1. P1：DropAll 仍可越过已验证的 schema 范围

位置：[PostgreSQL schema_operations.go](../../postgres/schema_operations.go:39)、[framework 范围校验](schema/drop_all.go:39)。

metadata 中的 schema 和对象名先被拼成 `schema + "." + name`，再交给会拆分点号的 Table wrapper。实库在 schema `s` 中创建表 `"outside.victim"`，另在 schema `"s.outside"` 中创建表 `victim`；当前范围只包含 `s`。`DropAllTables()` 返回成功，实际却删除了 `"s.outside"."victim"`，范围内原表保留。两表没有依赖关系，这不是 CASCADE 的依赖删除。

SQL Server [schema_operations.go](../../sqlserver/schema_operations.go:32) 也将点名对象编译成三段名称；MySQL [schema_operations.go](../../mysql/schema_operations.go:37) 对真实可创建的点名表生成三段 DROP，导致语法失败。Views/Types 有相同拼接方式。

这是基线编译器的问题，新范围检查仍不能保证最终 SQL 的目标范围。应把 catalog 提供的 schema、对象名分别作为原子标识符引用，不能重新解释分隔符。

证据：`/tmp/gradii_deep_schema_review/probe_scope.go`（PG 实库）、`quote_probe.go`（各方言编译）。

### 2. P2：Enum.Change 不处理前置 RenameColumn

位置：[PG planner](../../postgres/schema_enum.go:62)、[SQL Server planner](../../sqlserver/schema_enum.go:68)。

在同一个 Blueprint 中，先把 `status` 改名为 `state`，再把枚举允许值从 `a,b` 扩充为 `a,b,c`。planner 在 rename 执行前就用新列名查询 catalog。PG 因查不到旧约束而新增约束，旧约束仍限制 `a,b`：迁移返回 nil，插入 `c` 失败。SQL Server 的预检查直接报 `Invalid column name 'state'`。

这是新 planner 的命令组合缺口。需要按命令顺序追踪迁移前后列身份，不能把迁移前 catalog 当作所有命令执行后的状态。

证据：`/tmp/gradii_deep_schema_review/probe.go`，两个数据库均实测。

### 3. P2：SQL Server JSON 数字路径更新会静默近似

位置：[query_json.go](../../sqlserver/query_json.go:88)。

超出 `decimal(38, scale)` 可表示范围时直接改用 float。通过当前 Builder 传入有效的 `types.JSON`，没有编译错误；SQL Server 实测：

- `123456789012345678901234567890123456789` 变成 `1.234567890123457e+038`。
- `1e-400` 变成 `0.000000000000000e+000`。

这是新增的有损转换。JSON 文本本身能保存这些字面量；无法精确完成路径更新时，应报告明确错误，或另行提供明确的近似策略。

证据：`/tmp/gradii_json_number_review.go`，仅执行隔离的 SQL Server SELECT。

### 4. P2：标准库时间的自动赋值失败且错误被忽略

位置：[applyInsertTimestamps](gorm/write.go:119)、[更新模型回写](gorm/query.go:2634)。

自动时间统一创建 `*carbon.DateTime`，但 `schemaField.Set` 不能把它赋给 `time.Time` / `*time.Time`，调用方又忽略 Set 错误。因此标准库时间自动 Create 为零或 nil；自动 Update 即使数据库已更新，调用者持有的模型仍保留旧时间。

这是新增兼容测试揭示的现存缺口。应按目标字段类型转换，并传播或处理赋值错误。

### 5. P2：Save 的自动更新时间把零值和 typed nil 当作已提供

位置：[saveAssignments](gorm/update.go:114)、[addAutoUpdateTime](gorm/update.go:266)。

Save 把模型所有字段放入 assignments 后，仅凭 `value != nil` 判断时间是否已提供。零值 `time.Time` / `carbon.DateTime` 和装在 interface 中的 typed nil 都会绕过自动填充。四种字段形态在清空 UpdatedAt 后 Save，均未得到新的自动时间。

这是现存缺口。需要明确 struct 零值与显式 map 赋值的语义，不能以 interface 是否为 nil 代替字段是否有时间值。

### 6. P2：值切片批量 Create 丢失可寻址性，自动时间戳未写入

位置：[insertRecords](gorm/write.go:48)。

遍历 slice 时调用元素的 `Interface()` 再递归，把可写字段变成不可寻址的 struct 值，时间戳 setter 没有写回目标。即使调用者传入 `&[]Model{...}`，四种时间形态的批量自动时间戳仍失败。Carbon 的 `[]*Model` 对照通过；标准库时间指针切片还受到第 4 项影响。

这是现存批量写入缺口。提取记录时应保留原元素地址，或在可寻址副本上完成时间填充并正确生成绑定。

### 7. P2：Unicode 默认值修复遗漏文本指针和字节

位置：[SQL Server schema_literals.go](../../sqlserver/schema_literals.go:18)。

直接 string 会添加 `N` 前缀，但 `Default(&text)` 和 `Default([]byte(text))` 仍落入普通引号分支。`text="中文"` 时，缺省 INSERT 后实测读回 `"??"`。通用默认值逻辑本来接受这些输入形态，因此应先规范化文本，再应用 Unicode 引号，并保留 Expression 的独立语义。

这是旧 Unicode 问题在新 renderer 中覆盖不完整。证据：`/tmp/gradii_deep_schema_review/probe_unicode.go`。

### 8. P2：PostgreSQL DropAllTypes 对 CASCADE 删除的 domain 再次删除

位置：[schema_operations.go](../../postgres/schema_operations.go:76)。

创建 enum `state` 和基于它的 domain `state_domain`。DropAllTypes 先执行 `DROP TYPE state CASCADE`，domain 随依赖被删除；随后 `DROP DOMAIN state_domain` 报不存在，整个事务回滚，两个对象最终都保留。

这是基线遗留问题。应处理依赖删除顺序或已被 CASCADE 消费的对象。证据：`/tmp/gradii_deep_schema_review/probe_types.go`。

### 9. P2：默认 ORM 入口没有参与新的连接失效检查

位置：[Orm.Query](internal/orm.go:190)、[仅命名入口的检查](internal/orm.go:118)。

实测同名 DB/ORM 建立连接后执行 DB.Reconnect，新的 DB 正常，但原 facade 的 `Orm.Query()` 仍返回 `sql: database is closed`；显式 `Orm.Connection("").Query()` 才恢复。新的 cache generation 检查只覆盖 Connection(name)，默认 facade 仍保留旧 query。

这是生命周期修复仍遗漏的既有入口。默认 facade 与显式连接选择应具有一致的更新规则，同时保留调用者显式持有的 active Tx。

### 10. P2：共享 ORM cache 会把已取消的请求 context 留给后台调用

位置：[缓存创建](internal/orm.go:127)、[Background 时跳过重绑](internal/orm.go:196)。

实测先通过 `orm.WithContext(requestCtx).Connection("other")` 使用连接，取消 requestCtx 后，再通过根 `orm.Connection("other").Query()` 调用，仍报 `context canceled`。根 context 是 Background，Query 因此跳过 WithContext，继承了缓存 query 的旧请求 context。

这是已有的跨请求 context 缺口。共享缓存应保存不属于某个请求的连接状态，每次查询都绑定当前调用者的 context，包括 Background。

第 9、10 项共同证据：`/tmp/gradii-deep-review-alias/main.go`、`result.log`；仅使用临时 SQLite 文件。

### 11. P2：SQLite 删除后重建同名列，会静默跳过新 UNIQUE 索引

位置：[schema_rebuild_state.go](../../sqlite/schema_rebuild_state.go:157)。

合法的同一次迁移：`DropColumn("label")`、重新 `String("label").Nullable()`、`Unique("label").Name("requested_index")`。重建返回 nil，新 label 列存在，但 UNIQUE 索引没有创建。Go SQLite 3.53.4 实测插入两个相同 label 均成功。

新索引状态处理把“引用已删除旧列”的过滤也应用于调用者本次新增的索引，无法区分旧列和新建的同名列。这是本轮新增的数据约束缺口。应按命令顺序与列身份判断旧索引是否失效，同时执行新的索引声明。

证据：`/tmp/sqlite_schema_replace_unique_probe.go`。

### 12. P2：SQLite 重建后的 SELECT 校验不能保证保留 view/trigger 依赖有效

位置：[schema_rebuild_state.go](../../sqlite/schema_rebuild_state.go:230)。

新策略启用 legacy_alter_table 后，最后只执行 `SELECT * FROM view LIMIT 0` 验证视图。当前默认 Go SQLite 3.53.4 中，`items(id,label)` 上的 `SELECT *` view 如果有向 items(id,label) 插入的 INSTEAD OF INSERT trigger，删除 label 后迁移返回 nil，但后续向 view 插入报 `table items has no column named label`。SELECT 校验没有触及保留下来的 trigger 写依赖。

同一校验策略在受支持的自定义 Connector 启用 `DBCONFIG_DQS_DML` 后，还有静默错误：`CREATE VIEW labels AS SELECT "label" FROM items`，删除 label 后迁移提交，视图结果由原值 `kept` 变成常量 `label`。默认 DQS 关闭时这个特定场景会正确失败回滚，因此不能把它描述为默认配置行为。

这是新名称交接策略暴露的依赖验证缺口。需要可靠检查视图与 trigger 的列依赖，不能把一条 SELECT 能成功准备等同于语义保持不变。

证据：`/tmp/sqlite_schema_view_trigger_probe.go`（默认 Connector）、`/tmp/sqlite_schema_view_dqs_connector_probe.go`（显式 DQS Connector）。均使用临时 SQLite 数据库。

## 新增时间戳测试

- [单元测试](gorm/timestamps_review_test.go:74)：20 个叶子子用例，10 通过、10 失败。
- [独立模块实库测试](../tests/timestamps_review_test.go:91)：四数据库共 144 个叶子子用例，88 通过、56 失败，无 skip。现有 CNB integration 的 `go test ./...` 会执行这些用例。

| 功能 | carbon.DateTime | *carbon.DateTime | time.Time | *time.Time |
| --- | --- | --- | --- | --- |
| 自动 Create | 通过 | 通过 | 失败 | 失败 |
| 手动 Create / Save / Update | 通过 | 通过 | 通过 | 通过 |
| 自动 Save（UpdatedAt 清零） | 失败 | 失败 | 失败 | 失败 |
| 自动 Update，含原模型回写 | 通过 | 通过 | 失败 | 失败 |
| 值切片批量自动 Create | 失败 | 失败 | 失败 | 失败 |
| 指针切片批量自动 Create | 通过 | 通过 | 失败 | 失败 |
| 按 Scanner 契约读回 NULL | 通过 | 通过 | 通过 | 通过 |

Carbon 值类型直接调用其上游 `Scan(nil)` 会保留旧值；测试明确验证该 Scanner 契约，不强行改成框架清零。指针字段读回 SQL NULL 必须置 nil，标准库时间值字段清零。MySQL 非自增主键 Create 后 ID 回写的已有问题未混入 Save 时间戳断言，测试先读取实际模型再执行 Save。

失败断言保留，未修改实现来隐藏结果。详细日志：`/tmp/gradii_timestamp_unit_review_final.jsonl`、`/tmp/gradii_timestamp_native_review_final.jsonl`。

本次没有重新跑此前已通过的全量集成来替代边界复现；新增时间戳用例目前会使对应测试命令失败。通用嵌套事务已知问题仍见 [TRANSACTION_FOLLOWUP_PLAN.md](TRANSACTION_FOLLOWUP_PLAN.md)。
