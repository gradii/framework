# Laravel 数据库移植交接

状态：计划范围内的实现与验收已完成（2026-10-24）。不要按旧交接重新执行移植。
仓库：/Users/dog/Gradii/goravel-framework，分支 master；移植前基线 17ce480b。
实现、mocks、测试与文档使用同一批提交；提交号可用 git log -1 查看。没有推送远端。

## 用户约束

- 以本地 Laravel 12.x（0fc645c673）实际行为为准；上游目录 /Users/dog/OpenSource/framework。
- ORM 功能不在范围内；只保留必要的共享驱动兼容。
- DB fluent clauses 原地修改并返回同一对象，独立分支使用显式 Clone。
- 修改型 nested/subquery/BeforeQuery 回调忽略替换返回值；When/Unless/Pipe 保留返回语义。
- Paginate + 轻量 SimplePaginate + 原有 CursorPaginate；所有分页均无 URL、path、链接或 HTTP 参数。
- 错误统一 errors/list.go；mocks 只能 go tool mockery 生成。
- 两个仓库均有 .codegraph，定位代码先用 CodeGraph。没有授权不创建子代理。

## 已完成

- DB 查询入口、Builder 剩余 API、子查询、JoinClause、写入和 SQL 预览。
- 公共查询类型、状态、表达式及值处理工具位于 database/query/core；Builder 和 Grammar 直接调用 core 工具，不使用内部 helper
  转发文件。
- query/types.go 与 grammars/core.go 已移除，查询描述类型、常量和工具直接引用 core；Builder、编译器、DB/ORM 适配层及相关测试同步调整。
- 可变查询语义、回调、Sole/Lazy、基础分页、聚合空值及克隆编译错误传播。
- 连接会话、事件/日志、pretend、sticky、名称管理、版本和转义、reader 候选与读取恢复。
- 同连接 savepoint、事务层级、重试、提交/回滚回调、异常清理。
- 新 Processor 和 Schema 元数据实际接通 writer；限定名称、NULL、生成列、索引和外键。
- DDL 扩展、固定连接临时表、外键恢复、SQLite 重建及 Unicode/缺失列处理。
- MariaDB 元数据独立期望，实际验证 spatial SRID 和 vector。
- 单连接事务在 BEGIN 前解析版本；Pretend 在回调之前解析能力，回调内不执行真实 SQL。
- 修复锁集成测试的固定等待：显式等待全部事务并收集错误。

## 必须保留的上游细节

- forPageBeforeId/forPageAfterId 对 null ID 使用 whereNotNull。
- SqlServerProcessor 对 float、datetime2、datetimeoffset、time 使用 precision 展示类型。
  例如 datetime2(23) 是当前上游行为，不要自行改成 places。
- MariaDB 保留整数显示宽度、排序规则、JSON=longtext、SQL 默认值字符串和默认外键 restrict。
  tests/utils.go 只调整测试期望，不修改实际返回元数据。

## 验证完成

- Go 1.27 / Go 1.26.2 根模块全量；最后 DB 修改另经相关包测试与 race。
- database/db、driver、schema、query 相关 race。
- Linux amd64 四驱动完整 tests 模块：MySQL、PostgreSQL、SQLite、SQL Server 2022，退出码 0。
- MariaDB 11.8 / PostgreSQL / SQLite 完整 tests 模块；最终事务和锁回归也通过。
- 增量 golangci-lint（对比 17ce480b）：0 issues。全量 lint 的范围外基线问题未混入本次修改。
- PostGIS/pgvector 外部扩展实例未单独运行；实现及依赖边界明确记在测试映射中，不算实库验证。

详见 LARAVEL_DB_TESTS.md、query/LARAVEL_TESTS.md、query/PARITY.md。
原始日志在 /tmp/goravel-laravel-port-*.log，包含 root、go126、race、amd64、mariadb、lint-diff。

## 测试环境提示

- 颜色快照测试在各自 TestMain 中固定 ANSI 模式；可直接运行 go test -timeout 10m ./...，无需取消 NO_COLOR。
- MariaDB 测试：GORAVEL_TEST_MARIADB=1；使用独立容器缓存键，并检查 ServerVersion。
- macOS ARM 可以 GOOS=linux GOARCH=amd64 CGO_ENABLED=0 交叉编译 tests 二进制。
- amd64 运行环境：moby-ci 容器，--user 0 --network host，挂载 Docker socket、仓库与二进制。
- 容器需 ZONEINFO 指向 Go 的 lib/time/zoneinfo.zip；持久化 /tmp 测试缓存，避免重复启动数据库。
- 本机资源有限时用 -test.parallel=1；SQL Server 测试实例限制为 2048 MB。
- 不并行启动多个会清理同一批数据库的完整集成测试。测试后资源回收可能比用例本身慢。
