# 数据库重构审查修复计划与执行记录

日期：2026-10-28。用户已批准先提交五个仓库的基线改动，再执行本计划。第 1–9 节保留审批时的基线观察与设计说明；实际实现与验证记录见第 10 节。本次检查点保存修复、审查记录和时间戳回归测试；待修问题见 [DEEP_REVIEW.md](DEEP_REVIEW.md)。

Laravel 参考：`/Users/dog/OpenSource/framework`，12.x，提交 `0fc645c673`。下面区分 Laravel 已有行为、本项目保留的扩展，以及 Laravel 同样存在问题、需要按原生数据库规则修正的行为。已通过的旧测试不代表这些边界已经覆盖。

## 1. JSON：审查时的 Value / Scan 支持情况

有。它们来自 Go 标准库，与 GORM 无关，继续使用这两个接口即可，不需要定义泛型驱动接口：

```go
// database/sql/driver.Valuer
Value() (driver.Value, error)

// database/sql.Scanner
Scan(src any) error
```

当前写入链路是模型字段 → SQL bindings → `database/sql` → `Value()`；读取链路是 SQL rows → framework scanner → `Scan()`。

源码依据：

- [模型字段提取](gorm/write.go:94)、[执行绑定](gorm/executor.go:239)、[通用绑定保留原值](query/builder.go:308)。
- [读取时指针分配、NULL 与 Scanner 分派](internal/scan/value.go:17)。
- 框架自己的 [orm.DeletedAt](orm/deleted_at.go:25) 已经使用同一组标准接口。

临时 SQLite 程序 `/tmp/gradii_json_value_scan_probe.go` 的实测结果如下。这些结果尚不能代替四种数据库的完整验证。

| 场景 | 当前结果 |
| --- | --- |
| 用户示例的 `Doc *UserData`，两个方法均为指针接收者 | Create、First、Update 成功，实际调用 Value / Scan |
| 自定义具备 Value / Scan 的命名 JSON map | Create、First、Update 成功 |
| SQL NULL 读入已有 `*UserData` 字段 | 字段被置为 nil |
| JSON 文本 `null` 读入 `*UserData` | 当前字段仍是非 nil 指针；不能等同 SQL NULL |
| nil `*UserData` 用示例的 Value 写入 | 方法返回 JSON 文本 `null`，存储结果是 JSON null，不是 SQL NULL |
| Scanner 返回错误 | 错误传回调用方，`errors.Is` 可识别原始错误 |
| `Doc UserData` 值字段，但只有 `(*UserData).Value()` | 写入失败：绑定的是 UserData 值，没有取得其指针方法 |
| 实现 Valuer 的命名 slice，更新整列 | MySQL、PostgreSQL、SQLite 提前 json.Marshal，绕过 Value；SQL Server 此路径保留 Valuer |

三个概念需要分开：

- `gorm:"type:json"` 是建表/迁移类型提示，不会自动把任意 Go struct 序列化。
- `gorm:"serializer:json"` 是 GORM 的字段序列化功能，当前框架没有实现。示例能工作是因为标准接口，不是这个 tag。
- `gorm.io/datatypes.JSONMap` 实现标准接口，但引入该模块会带入 GORM 相关依赖。迁移目标应使用自有类型或业务类型自己的 Value / Scan。

示例中的 Scan 还需要改进：处理 `string`、`[]byte`、nil，拒绝不支持的输入，传回非法 JSON 错误；读取时清理旧值，避免重复读取留下旧字段。`json.Unmarshal(data, &r)` 中的 `&r` 是局部接收者变量的地址，不能可靠地把模型字段变为 nil；通常应解码到临时值，成功后赋给 `*r`。目前 framework 会先把 string 转成 []byte，所以不能据此认为这个 Scan 在直接使用 database/sql 时也完整。

## 2. JSON 的拟定实现范围

### 2.1 以标准接口为优先的统一读写规则

整列赋值时，已经实现 `driver.Valuer` 的值保持为一个 SQL 参数；自定义 map、slice、命名 byte slice 都不能先被通用集合处理改写。只有普通 Go 集合才适用当前 Laravel 风格的自动 JSON 编码规则。

模型写入增加专用字段绑定提取逻辑：字段本身实现 Valuer 时直接使用；只有 `*T` 实现时取得字段地址；不可寻址的模型值通过副本提供该方法。不要全局改动关系解析使用的 `ValueOf` 返回类型。Create、批量 Create、Save、Update 共用此规则。直接传给底层 SQL API 的业务值继续遵循 Go 方法集规则：只有 `*T` 实现时，调用者应传 `*T`。

保留 database/sql 对 typed nil Valuer 的规则，不把所有 nil 指针提前统一替换为 SQL NULL。若业务方法会接收 nil，它应明确选择返回 `(nil, nil)` 表示 SQL NULL，或返回 JSON 文本 `null`；示例的 json.Marshal(r) 选择后者。

JSON 路径更新和 `WhereJsonContains` 是独立的方言操作，不能把“具备 Valuer”解释成“支持任意 JSON 包含结构”。对需要把参数作为 JSON 文档处理的路径，采用统一、可返回错误的准备步骤：尊重 Value 的结果，再按该方言明确区分 JSON 文档与字符串，避免二次编码、数组展开和编译/执行占位符不一致。此步骤与 4 节一起实现、验收。

读取继续由 Scanner 决定自定义类型的解码。拟将通用 Scanner 分派改为传递数据库原始 `string` / `[]byte` / nil；现有时间类型兼容转换应限定在相应类型内。这样直接 SQL Scan 与 ORM Scan 的输入契约一致。**兼容影响：当前依赖 framework 把 string 自动转成 []byte 的自定义 Scanner，应补上 string 分支，包括用户给出的示例。**

SQL NULL 读入指针字段保持 nil；非指针 Scanner 接收 nil 并决定零值。模型属性赋值路径也需核对 `schemaField.Set` / `scanSourceInto`，避免将 Scanner 错误折叠成 false 后变成无关的类型错误。相同类型的 Go 值直接赋值仍保留，不为了测试强行增加一次 JSON 往返。

修改范围：`database/gorm/{write,update,model_schema}.go`、`database/internal/scan`、`database/query/{compile,core}`，以及四个 driver 的 JSON binding 分支。SQL 方言继续留在 driver；公共接口、模型处理留在 framework。

### 2.2 提供不依赖 GORM 的常用 JSON 类型

建议新增 `github.com/gradii/framework/database/types`，提供 `JSON` 与 `JSONMap`，均实现标准 Value / Scan 和适当的 JSON marshaling；业务 struct 继续直接实现 Value / Scan。第一阶段不增加泛型 wrapper 或完整 serializer 注册系统。

拟定契约：

| 值 | 数据库存储/读取语义 |
| --- | --- |
| `types.JSON(nil)` | SQL NULL |
| `types.JSON` 内容为 `null` | JSON null，与 SQL NULL 可区分 |
| 有效 JSON 对象、数组、标量 | 验证后以 JSON 文本绑定；读取复制驱动字节缓冲区 |
| 非 nil 的空 JSON 字节串、非法 JSON | 返回错误，不自动变为 NULL 或空对象 |
| `types.JSONMap(nil)` | SQL NULL |
| 非 nil 空 JSONMap | `{}` |
| JSONMap 读取 SQL NULL 或 JSON null | nil map；需要区分两者时使用 types.JSON |
| JSONMap 读取数组或非 null 标量 | 返回类型错误 |

JSONMap 的 NULL 约定是本项目的明确契约，不宣称与 gorm.io/datatypes 所有历史行为相同。扫描到无效输入时保留旧值并返回错误；有效输入完整替换旧值。四个数据库必须覆盖 Value 返回 string 和 []byte 的业务自定义类型。

推荐模型写法：

```go
type User struct {
    orm.Model
    Json1 types.JSONMap `gorm:"column:json1;type:json" json:"json1"`
    Json2 *UserData     `gorm:"column:json2;type:json" json:"json2"`
}

func (User) TableName() string { return "users" }
```

其中 UserData 使用完善后的 Value / Scan。显式 table/column/JSON 字段名与 Go struct 名解耦，适合既定混淆需求；不要恢复 struct 名命名策略。

`serializer:json` 不作为本轮新增序列化机制。已经具备标准接口的字段可以保留这个冗余 tag；仅依赖该 tag、没有标准接口的字段需要在模型解析时明确报不支持，不能静默漏写。若后续要支持无需实现方法的任意 struct，再单独设计字段 cast API。

Laravel 对照：[HasAttributes::castAttribute](</Users/dog/OpenSource/framework/src/Illuminate/Database/Eloquent/Concerns/HasAttributes.php:826>)、[castAttributeAsJson](</Users/dog/OpenSource/framework/src/Illuminate/Database/Eloquent/Concerns/HasAttributes.php:1352>) 与 [CastsAttributes](</Users/dog/OpenSource/framework/src/Illuminate/Contracts/Database/Eloquent/CastsAttributes.php:11>) 把属性转换留在模型层。Go 端通过标准接口实现同一职责划分，不照搬 PHP cast 类或 GORM serializer。

验收包括：两种接收者方法集、指针与值字段、命名 map/slice/byte slice、批量/事务/Save/Update、NULL 与 JSON null、空对象/空数组、中文、非法 JSON、Valuer/Scanner 原始错误、重复读取旧值清理；同时保留时间、软删除、原始 bytes 和枚举绑定测试。

## 3. P1：DropAll 操作必须使用正确的数据库范围（本次回归）

现状：MySQL 的 DSN-only 数据库名可能在配置中为空，最终把空名称解释为全部非系统库；DSN 覆盖配置时也可能选中旧数据库。PostgreSQL 的 `$user` 可能因用户名只存在于 DSN 中而展开为空；Connector-only 的实际 search_path 也可能被配置默认 public 遮蔽。Tables、Views、Types 的相应路径均需覆盖。

拟定方案：

1. MySQL 实现现有 `SchemaCurrentSchemas` 接口，通过当前 writer 的 `SELECT DATABASE()` 取得实际库名；没有选中数据库时返回命名错误。
2. PostgreSQL 用当前 writer 的 `current_schemas(false)` 解析实际有效 search_path，让服务端处理 `$user`、role、权限与不存在的 schema，不用字符串替换猜测当前身份。
3. 结构化连接配置中的 search_path/schema 必须与 connector 实际应用的参数统一。保留现有 Connector → DSN → 分项配置的连接来源优先级；配置中的默认 public 不得覆盖 DSN/Connector 的真实 session。包含逗号的 quoted schema 名不能简单 strings.Split。
4. framework 将“取得范围 → 读取对象 → 编译并执行 DropAll”包在同一个 `WithWriteConnection` session 内。过滤条件始终带明确名称；在执行前验证 metadata 返回的对象属于已解析范围。
5. 未知范围/空名称返回错误；PostgreSQL 成功解析为无有效 schema 时安全无操作。空集合绝不能传入表示“列出所有 schema”的 metadata 查询。保留公开 `GetTables()` 等无参数浏览全部 schema 的能力，将其与 DropAll 的已解析范围区分。

范围：`mysql/schema_metadata.go`、`postgres/{config,postgres,connector,schema_metadata}.go`、framework `database/schema/{schema,metadata,operations}.go`。优先复用现有 SchemaCurrentSchemas / SchemaCatalog，不增加具体数据库判断到 framework。

Laravel 依据：[MySqlBuilder](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/MySqlBuilder.php:12>) 的 DropAll 限定当前数据库；[PostgresBuilder](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/PostgresBuilder.php:89>) 按 search_path 选择范围。Laravel 使用配置中的数据库身份；本项目支持任意 Go Connector，必须额外从实际 session 取得身份，不能原样复制其配置推导。

验收：DSN-only、Connector-only、DSN 与分项配置冲突、`$user`、不存在的 `$user` schema、多 schema、quoted 名称、writer/reader 分离、连接内切换 search_path。使用隔离测试资源在范围内外各建对象，验证范围外对象保持不变；未知范围确保零条破坏性 SQL。PostgreSQL 保留现有 CASCADE 行为，其对跨 schema 依赖的影响需在文档中明确；上述范围保证针对直接列出的删除对象。

## 4. P1：SQL Server JSON contains 崩溃与绑定错误（本次回归）

现状：map 被当作数组、又转成只包含自身的 slice，无限递归造成整个进程 stack overflow；二维列表产生占位符/绑定数量不一致；bool slice 被原地修改。

Laravel [SqlServerGrammar::compileJsonContains / prepareBindingForJsonContains](</Users/dog/OpenSource/framework/src/Illuminate/Database/Query/Grammars/SqlServerGrammar.php:172>) 使用 scalar membership，仅转换 bool。[对应测试](</Users/dog/OpenSource/framework/tests/Database/DatabaseQueryBuilderTest.php:6582>) 覆盖 bool、string、顶层 Raw。本项目旧 driver 和当前测试另外支持一层标量列表，保留这一扩展。

| 输入 | 计划行为 |
| --- | --- |
| 标量、顶层 Expression | 保留；bool 绑定为 true/false 文本 |
| 一层标量 slice/array，包括 typed slice 与 []any | 每个元素一个谓词，按 AND 组合；否定时对整个组合取反 |
| 空列表 | 保留正向 true、否定 false 的项目约定 |
| map、命名 map、slice-of-map、嵌套 slice/array | 在编译准备阶段返回命名错误，不递归、不执行 SQL |
| nil、bytes、enum、可绑定的 Valuer | 先按其标量绑定契约识别，不按 reflect.Kind 粗暴拒绝 |
| 列表内 Expression | 不新增该能力，返回明确错误；顶层 Expression 继续支持 |

修改 `sqlserver/query_grammar.go` 及必要的 builder 验证入口，将值分类、SQL 编译与 binding 准备使用同一套规则。禁止修改输入 slice，不能把 json.Marshal 成功等同于当前 dialect 支持该参数。

验收：所有拒绝场景无 panic、无数据库调用；支持场景占位符数/绑定数/顺序一致；输入可复用于其他 builder/driver；保留标量、列表、NOT、空列表和 Raw 原有有效测试。对象包含语义不在这一修复中扩展。

## 5. P2：连接 alias 的 Purge / Reconnect（本次回归）

现状：pool 按 driver 的 canonical Connection 缓存，Purge 却按调用者 alias 删除；返回成功但旧连接仍可写，Reconnect 可能取回同一旧池。

Laravel [DatabaseManager::connection](</Users/dog/OpenSource/framework/src/Illuminate/Database/DatabaseManager.php:93>) 以完整逻辑名称缓存；[purge](</Users/dog/OpenSource/framework/src/Illuminate/Database/DatabaseManager.php:299>) 删除指定项；[reconnect](</Users/dog/OpenSource/framework/src/Illuminate/Database/DatabaseManager.php:325>) 重建/刷新相同名称的连接。Laravel 的 `foo::read` 等完整名称是独立缓存项，与我们通过 via 返回同一 driver 的共享池 alias 不完全相同。

建议采用与 Laravel 一致的逻辑名称生命周期：不同 alias 各有独立池。内部用 `(配置身份, 请求逻辑名)` 这样的统一身份区分资源，保留同名 alias 指向不同配置时的隔离；DB 与 ORM 对同一个逻辑名仍共享资源。不因这个 bug 自动新增 Laravel 的 `::read` / `::write` 别名语法。

修改覆盖 writer、reader、telemetry、版本缓存及 DB/ORM/Schema 各入口。ORM BuildQuery 应保留请求逻辑名，并通过 Open 返回的连接句柄/内部身份取得 reader、instrument；不要把复合缓存键写进对外 Config.Connection，否则会破坏配置路径与模型连接选择。Purge 关闭并清除指定逻辑连接，Reconnect 在相同身份下重建/刷新连接资源。

这是需要 review 的策略变化：当前新增的 `TestOpenScopesSharedAliasesToConfiguredConnections` 同时断言“不同配置同 alias 隔离”和“同配置不同 alias 共享池”。保留前者，把后者 port 成“不同 alias 生命周期互不影响”，保留测试用例。代价是每个 alias 分别占用连接池；相比维护反向 alias 映射后整组失效，更接近 Laravel，也避免 purge 一个名称连带关闭其他名称。

验收：从 alias 建连后 purge 真正关闭目标旧池，reconnect 得到新池；其他 alias 保持可用；同名但不同配置隔离；DB/ORM/Schema 同逻辑连接资源一致；默认连接与显式名称一致；并发 open/purge/reconnect 不遗留失效缓存。

## 6. P2：SQLite 重建表（旧集中实现遗留）

修改集中于 `sqlite/schema_rebuild.go`，framework 仅在确实需要时扩展通用 session 生命周期。

| 问题 | 拟定修复 | 验收 |
| --- | --- | --- |
| 重命名本表 id 误改 `REFERENCES parent(id)` | 结构化区分本地列、外部引用列；只对自引用同步调整目标列。保留原约束子句及标识符引用 | 外部 FK 与自引用 FK，inline/table-level 两种定义；迁移后实际插入、删除并验证约束 |
| 重建丢失 sqlite_sequence 高水位 | 同一 session/事务内保存旧序列，重建后保留 AUTOINCREMENT 时恢复旧值与新值的较大者 | 删除过最大 ID、空表、连续重建、表改名、attached 库；新 ID 大于历史高水位 |
| Change 与 RenameIndex 混用时忽略 rename | 按 Blueprint 命令顺序维护索引状态，重建后生成最终索引；复用原定义以保留 unique/partial/expression 语义 | 两种命令顺序、多个 rename、索引实际名称和查询/约束效果 |
| 引用该表的 view 导致临时表改回原名失败 | 在固定 session 上为名称交接保存并临时设置 legacy_alter_table，结束时恢复原值；提交前验证受影响 view | 原有 view 迁移后可查询；失败/取消回滚，原 pragma 恢复，无临时表/无效 view 残留 |

Laravel 依据：[BlueprintState](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/BlueprintState.php:191>) 区分外键本地列，并在 213 行更新索引名；[Blueprint 命令处理](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Blueprint.php:278>) 与 [重命名后 Change 测试](</Users/dog/OpenSource/framework/tests/Integration/Database/Sqlite/DatabaseSchemaBlueprintTest.php:28>) 可作为顺序参考。

Laravel [SQLiteGrammar 重建序列](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Grammars/SQLiteGrammar.php:324>) 同样没有完整处理 sequence 高水位与 view 名称交接，这两项以 SQLite 实际行为为准。view 的列被删除或无法安全改写时，迁移应在提交前失败并回滚；不承诺通过任意字符串替换自动重写复杂 view。相比批量 drop/recreate views，优先使用最小 session 设置，避免丢失 view 的 INSTEAD OF triggers。

若使用 session 设置，扩展方言无关的 setup/cleanup 能力，由 driver 生成 SQL、framework 保证 defer 恢复；恢复不能只是普通 SQL 列表末尾的一条语句。恢复失败要报告并避免把状态不明的连接继续交回池。需要覆盖嵌套事务和旧版 SQLite 的实际支持情况。

## 7. P2：PostgreSQL / SQL Server Schema Change（旧集中实现遗留）

| 问题 | 拟定修复与 Laravel 对照 | 验收 |
| --- | --- | --- |
| PostgreSQL `StoredAs(nil).Change()` 顺序错误 | 先 DROP EXPRESSION，再按目标定义处理类型、NULL、DEFAULT、IDENTITY；参考 PostgresGrammar:281 的分步 ALTER，但修正其与实库冲突的顺序 | 实库 generated → normal、带/不带 default，数据保留；不支持该操作的版本明确报错 |
| SQL Server `UseCurrent().Change()` 丢默认约束 | fluent 默认值命令注册时不要依赖尚未由 TypeTimestamp 填充的 Default；在列类型/属性解析完成后决定 ADD DEFAULT | 无默认→有默认、已有默认→CURRENT_TIMESTAMP、再次 Change；插入缺省值实际得到时间 |
| SQL Server persisted computed 列忽略 NOT NULL | 显式 Persisted + Nullable(false) 输出 `persisted not null`；未指定或 true 不强加约束 | 可空/不可空 computed 的实际写入；无效 non-persisted 组合编译前报错 |
| SQL Server Unicode 默认值变成问号 | driver 的 schema literal renderer 对字符串使用 `N'…'`，正确转义；数字/bool 保留语义，Expression 不改写 | Latin1 collation 下中文/emoji/单引号默认值精确往返，Unicode enum 同步覆盖 |
| PostgreSQL、SQL Server `Enum(...).Change()` SQL 无效 | 将基础列类型与 membership CHECK 分开，在事务内定位旧约束、替换类型/约束；不把 inline CHECK 拼进 ALTER COLUMN 类型 | 扩充/缩减 allowed 值、NULL/default、已有无效数据失败回滚、多条 CHECK 并存、命名/未命名旧约束 |

准确参考与需要偏离之处：

- PostgreSQL [compileChange](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Grammars/PostgresGrammar.php:281>) 及 [storedAs(null) SQL 断言](</Users/dog/OpenSource/framework/tests/Integration/Database/Sqlite/DatabaseSchemaBlueprintTest.php:126>) 仍可能把 default 操作放在 DROP EXPRESSION 前；此前 PostgreSQL 18.3 实测拒绝此顺序，不复制错误断言。
- SQL Server 的默认命令参考 [Blueprint::addFluentCommands](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Blueprint.php:264>)、[compileDefault](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Grammars/SqlServerGrammar.php:323>)，可 port [UseCurrent + Change 测试](</Users/dog/OpenSource/framework/tests/Integration/Database/Sqlite/DatabaseSchemaBlueprintTest.php:141>)。
- [SQL Server nullable/default/generated 修饰](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Grammars/SqlServerGrammar.php:974>) 与 [通用默认值字符串](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Grammars/Grammar.php:476>) 也没有补全 computed NOT NULL、Unicode 前缀，需要原生数据库补强。
- [PG typeEnum](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Grammars/PostgresGrammar.php:943>)、[SQL Server typeEnum](</Users/dog/OpenSource/framework/src/Illuminate/Database/Schema/Grammars/SqlServerGrammar.php:735>) 采用内联 CHECK；其 Create 语法不能原样复用到 Change。

Enum 约束识别是这一组中最复杂的部分：通过 catalog 获得约束名称/表达式，识别框架生成的 membership 约束；保留用户其他 CHECK。新建约束采用由显式表/列名生成的稳定、可识别名称，不依赖 Go 类型名，并处理名称长度/冲突。旧约束不能唯一识别时，在执行任何 DDL 前返回命名错误，让迁移显式指明/处理约束；绝不删除该列相关的所有 CHECK。新增检查需要验证已有数据，失败完整回滚。沿用 varchar/nvarchar + CHECK 的设计，不趁本次改成 PostgreSQL 原生 enum 类型。

## 8. 实施顺序、测试与兼容边界

建议按可独立 review 的四批实现：

1. 修正 DropAll 范围与 SQL Server JSON 崩溃，保留原有有效查询能力。
2. 完善 Value / Scan、增加自有 JSON 类型与迁移文档，同时处理 alias 生命周期。
3. 修复 SQLite 重建与 PostgreSQL/SQL Server 简单修饰符，再完成 Enum Change 的约束处理。
4. 执行跨模块回归，更新对应文档和必要的 CNB 测试接入。

驱动单元测试验证 SQL/绑定及 metadata 解释；framework 单元测试验证 Scanner、模型映射、连接/session 生命周期；真实数据库回归放进现有独立 `tests` 模块或对应 driver 的集成测试。每个缺陷保留最小回归，并验证数据/约束实际效果，不能只更新 SQL 快照。

保留 framework 与四个 driver 的原测试。旧 GORM 用例 port 到新接口；如果旧断言表达的是数据库拒绝的 SQL，则调整断言为正确行为并说明依据，不恢复旧实现，也不删掉测试意图。

验证范围：五个根模块单元测试、相关 race/vet、四数据库集成测试。保留已有旧版本能力检测、MySQL/MariaDB 分支、charset/collation/timezone、读写分离回归。旧服务器不支持某项 JSON/DDL 特性时明确报错或走已有兼容路径，不能生成错误 SQL；未实际运行的版本标注未验证。

本地继续使用现有 go.work 解析尚未发布的五个仓库；不在 go.mod 固化开发者路径。CNB 沿用兼容 revision checkout + 临时 workspace；新集成用例必须被独立 tests 模块的 job 执行，不能因嵌套 go.mod 漏跑。相关 forks 尚未发布时继续由现有 URL/REF 配置提供源码，不假设 GitHub 已存在新代码。

所有新增错误声明到相应 `errors/list.go`，使用框架 New 构造器并保留底层错误。接口变化通过 mockery 更新 mocks。Docker/安装工具不在运行时迁移范围。

## 9. 建议 review 的设计选择

本计划建议采用：标准 Value / Scan 优先；新增非泛型 JSON/JSONMap；本轮不实现 serializer 注册系统；Scanner 恢复标准输入类型并在迁移文档说明兼容影响；SQL Server 不支持的 JSON 结构提前报错；DropAll 以真实 writer session 为准；复杂旧 Enum 约束识别不明时拒绝自动修改。

Alias 采用独立逻辑生命周期：同逻辑名的 DB/ORM 共用池，不同 alias 分别建池；这是相对原共享策略的明确调整。

## 10. 执行记录

已先提交基线：framework `cb1be4fb`、MySQL `3aa6460`、PostgreSQL `a4e59d5`、SQLite `bb7ba31`、SQL Server `ace134d`。这些提交包含此前的 driver 外移、测试迁移、CNB 配置及本计划；没有 push。

本计划中的 JSON 标准接口/类型、清库范围、SQL Server contains、连接 alias、SQLite 四项重建，以及 PostgreSQL/SQL Server schema 修复均已落地。实现时有以下补充：

- ORM 读写字段遍历复用 `database/internal/reflectx`；保留显式的表名、列名及 JSON 字段映射。
- 四库实测发现 `Value()` 的 JSON 字节结果需要区分文本与二进制。显式 `type:json/jsonb` 模型字段在调用标准 Valuer 后将有效 JSON 绑定为文本，覆盖 Create/Save/模型更新。没有恢复任意 struct 的 serializer。PostgreSQL 保留原 simple protocol，避免改变既有参数转换行为；直接 DB 参数继续遵循原生 driver 约定。
- JSON 路径操作对 Valuer 返回的有效 JSON 文本按文档处理，普通 SQL 文本按字符串处理；自有 JSON 类型仍负责报告无效文档。修复 JSON 数字精度/类型、调用方有序更新与 map 被修改、Valuer 被当作列表展开等相邻问题。
- SQL Server 不兼容的 Enum 收窄可能使服务器撤销整个事务，因此在同一事务中持锁预检查目标值、NULL 和长度，在 DDL 前报告不兼容数据；最终约束仍由数据库验证。
- SQLite session 恢复失败时废弃实际租借的物理连接；普通未租借的 active Tx 在任何 session 设置或 DDL 前明确拒绝。没有扩大到隐式 context 事务或并发嵌套分支支持。
- Schema 错误保留公开上下文消息，同时通过 Unwrap 保留底层错误，支持 errors.Is。

新增真实数据库回归位于独立 tests 模块的 `driver_scope_review_test.go`、`driver_sqlite_review_test.go`、`driver_schema_review_test.go`、`json_values_review_test.go`。现有 CNB integration job 的 `go test ./...` 会运行它们，无需另建模块或依靠未设置 DSN 时会 skip 的 driver 根模块测试。原测试保留；SQL Server Unicode 默认值、JSON 浮点文本等断言已按实际语义 port。

深审新增时间戳测试前的验证：framework 全量单元测试、四个 driver 全量测试、framework database 与四个 driver 的 race、相关 vet 均通过。`GOARCH=amd64 go test -count=1 -timeout 25m ./...` 在独立 tests 模块全量通过（336.633 秒），包含 SQL Server。原有旧版本 SQL 分支测试保留并通过，未逐个旧版服务器进行实库验证。

随后新增的四种时间类型测试暴露了未修兼容缺口：单元 20 项中 10 项失败，四库实测 144 项中 56 项失败。当前检查点保留这些失败用例，不能将上述早先验证结果解释为当前全部测试通过；详情见深审报告。

基线保留检查通过：framework 的 478 个测试文件、4,195 个测试/基准/示例函数，以及四个 driver 的基线测试均未删除。主要验证日志为 `/tmp/gradii-review-framework-unit-confirmed.log`、`/tmp/gradii-review-race-confirmed.log`、`/tmp/gradii-review-vet-confirmed.log`、`/tmp/gradii-review-integration-confirmed.log`。

新调查到的事务句柄/并发限制单独记录在 [TRANSACTION_FOLLOWUP_PLAN.md](TRANSACTION_FOLLOWUP_PLAN.md)，尚未实施其行为调整。另发现原 MySQL `json_overlaps` selector 分支沿用三参数形式，Laravel 参考版本也有此断言；这不在已批准修复项中，后续应改为对 `json_extract(field,path)` 执行双参数 overlaps，并保留相应测试。
