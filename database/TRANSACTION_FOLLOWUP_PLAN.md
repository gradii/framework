# 嵌套事务与 Go 并发：后续计划

状态：仅调查与方案，尚未实施这些事务行为调整。此次已批准的 driver review 修复继续使用现有 DB/Tx 路径；session cleanup 的实际 Discard 能力检查已经作为本轮修复补齐。

事务应绑定显式 Tx 句柄及同一物理连接。context 提供取消、deadline 和 tracing；共享 context 不会自动加入相同事务，也不提供 goroutine 之间的事务隔离。可使用派生 context，但不能借此获得一份独立 savepoint 栈。

当前调查确认：

| 路径 | 现状 |
| --- | --- |
| 基础 DB.BeginTransaction | 新建根 SQL 事务并占用连接；状态属于该事务实例 |
| 已有 DB Tx.BeginTransaction | 在同一 SQL 事务上建立 savepoint；内层 Commit 仅结束逻辑记录 |
| DB 事务管理器 | mutex 保护状态与 Begin/Commit 顺序；普通 SQL 的检查和执行不在同一保护范围 |
| 父 Tx 与活跃 child 混用 | 父 SQL 可以落入 child savepoint 范围并随 child 回滚 |
| 已提交的旧 child 再 Rollback | 目前按层级处理，可能回滚后来建立的 sibling |
| ORM 已在事务中再次 BeginTransaction | 返回原对象，没有 savepoint；内层 Commit/Rollback 会结束整个 SQL 事务 |
| Schema.WithWriteConnection | 只绑定连接 lease，不自动绑定 callback 中另行创建的 Tx |
| Schema 嵌套策略 | 仅在 Schema 已显式绑定实际 active Tx 时建立 savepoint；不支持从 context 隐式发现事务 |

临时原生 SQLite 验证程序：`/tmp/gradii_transaction_boundary_probe.go`。永久回归应在后续实现时移入仓库。不能用 `go test -race` 无告警代替上述事务语义测试。

建议分三步处理：

1. 修正句柄生命周期与作用域检查。Begin、普通 SQL、Commit、Rollback、RollbackTo、callback 注册都按具体 record 身份验证，不能仅依赖 level。已结束的 child 拒绝操作；父 scope 在 child 活跃时不能执行会混入 child 的 SQL。明确保留哪些主动祖先回滚操作。
2. 统一 ORM 与 DB 的事务管理。让 ORM 使用相同 manager/savepoint 实现；提供显式的 ORM/Schema Tx 绑定入口，业务层传递 Tx。禁止通过全局 map、goroutine ID 或共享 context 推断当前事务。Laravel 提供 savepoint 与回调顺序参考；Go 必须额外处理句柄生命周期和并发。
3. 增加操作级串行化或冲突拒绝。将状态检查与 SQL/savepoint/commit/rollback 放进同一个操作保护范围；流式 Rows 从创建到 Close/耗尽也需要明确所有权。不能锁住整个 Transaction callback，否则同一 goroutine 合法的嵌套调用会死锁。callback 在适当状态下移到锁外执行，避免回调重入死锁。

Schema session cleanup 必须同时适配新的作用域规则：当前恢复设置可能通过外层 lease executor 执行。不能单独在 `ready` 增加 `current == record`，却让合法恢复路径被拒绝；应在 active scope 内完成恢复，再结束该 scope。

验收必须使用确定性的通道同步，覆盖不同请求共享同一 context 仍拥有独立根事务、父子交错、两个 sibling 并发开启、旧 child 再次回滚、流式读取与 Commit 交错、取消期间恢复、callbacks 重入、ORM/Schema 显式绑定同一 Tx。另用真实数据库检查 savepoint SQL、外层提交与回滚范围，并运行 race 检查内存访问。

上述方案不承诺同一个 SQL 事务可在多个 goroutine 中并行形成独立嵌套分支。推荐的业务契约是顺序执行嵌套作用域；需要并行独立事务时，从 DB 分别开启根事务。
