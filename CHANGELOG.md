- # Changelog

  所有重要变更都会记录在这里。

  版本格式遵循：

  ```text
  v主版本.次版本.修订版本
  ```

  当前项目仍处于早期可用和持续验证阶段，离线恢复结果受达梦版本、页大小、字符集、表类型、行格式、数据页损坏程度等因素影响。

  ------

  ## v0.4.0

  ### Added

  - 新增标准两阶段 `bootstrap` 流程。
  - 第一阶段通过 `SYSTEM.DBF` page 0 的 anchor 定位核心系统字典入口：
    - `SYSOBJECTS`
    - `SYSINDEXES`
  - 第二阶段根据核心字典定位并下载扩展系统字典：
    - `SYSCOLUMNS`
    - `SYSTEXTS`
    - `SYSGRANTS`
    - `SYSHPARTTABLEINFO`
  - 新增结构化 `bootstrap` 日志。
  - `bootstrap;` 现在会在控制台和 `dul.log` 中记录：
    - SYSTEM.DBF 基本信息
    - page size / extent size / charset
    - DBF 文件 group/file/page 信息
    - SYSOBJECTS / SYSINDEXES anchor 信息
    - root page / storage id / leaf pages / rows
    - 核心字典表扫描方式
    - fallback 原因
    - 输出目录
    - 对象统计
    - 执行耗时
    - 最终状态
  - `dmdul_dict/meta.tsv` 新增 bootstrap 模式信息：
    - `bootstrap_mode`
    - `bootstrap_fallback`
  
  ### Changed
  
  - `bootstrap` 优先使用 `standard-two-stage` 标准字典下载模式。
  - 当 anchor、storage root 或 leaf chain 无效时，自动回退到原有按页流式扫描模式。
  - 空 TEMP 文件或可重建临时文件会标记为 `IGNORED_TEMP`，不影响最终成功状态。
  - `dul.log` 从简单命令日志升级为可诊断的恢复证据链日志。
  - `list user;` 会显示当前 bootstrap 模式和 fallback 状态。
  - README、使用文档和开发文档已调整为交互式流程优先。

  ### Removed
  
  - 移除功能性命令行子命令：
    - `inspect`
    - `inspect-ctl`
    - `scan-system`
    - `scan-partitions`
    - `export-ddl`
    - `export-data`
  - 移除仅用于旧 inspect 命令的 `internal/storage` 包。
  - 后续 DDL、数据导出、分区扫描、残留页恢复统一通过交互式 DUL Shell 执行。
  
  ### Breaking Changes
  
  - 从 v0.4.0 开始，`dmdul` 不再支持直接命令行导出 DDL 或数据。
  - 以下命令已废弃并移除：
  
  ```text
  dmdul export-ddl
  dmdul export-data
  dmdul inspect
  dmdul inspect-ctl
  dmdul scan-system
  dmdul scan-partitions
  ```
  
  请改用交互式流程：

  ~~~text
  dmdul
  DMDUL> bootstrap;
  DMDUL> unload database;
  DMDUL> unload user HIS;
  DMDUL> unload table SYSDBA.T1;
  DMDUL> recover table USERS1.T_TEST;
  ~~~
  
  ### Validation
  
  - `go test -count=1 ./...` 通过。
  - `go vet ./...` 通过。
  - oldpro 样例验证通过：
    - users: 6
    - tables: 28
    - columns: 122
    - rows exported: 12679
    - rows failed: 0
  - FEIGE 样例验证通过：
    - users: 125
    - tables: 3814
    - columns: 65253
    - routines: 115
    - bootstrap mode: `standard-two-stage`
    - fallback: `false`
  - 真实 `load dictionary; list table HR_TEST;` 流程验证通过。
  
  ### Notes
  
  - `standard-two-stage` 模式下，对象数可能比旧版本略少，因为旧版本在 root/internal 页中可能误识别少量分隔键对象。
  - 如果标准 anchor 或 root-chain 损坏，dmdul 会自动 fallback 到流式扫描，并在日志中记录原因。
  
  
  
  ## v0.3.0
  
  ### Added
  
  - 新增 `STORAGE(USING LONG ROW)` 表初步恢复能力。
  - 新增 21 字节 LOB locator 解析。
  - 新增从当前活动行追踪 `0x20` LOB 页和 `0x22` Long Row 页的读取能力。
  - 新增显式 2-bit 行 NULL metadata 解码。
  - 新增 metadata 优先的数据行解析路径，旧启发式解析保留为兜底。
  - 初步补充以下基础类型解析入口：
    - `REAL`
    - `FLOAT`
    - `DOUBLE`
    - `TIME`
    - 带时区时间类型
    - `INTERVAL DAY TO SECOND`
    - `ROWID`
  
  ### Changed
  
  - 数据行解析核心从主要依赖启发式判断，调整为优先使用显式 metadata。
  - 变长字段解析增强，支持识别普通变长值、内联 LOB envelope 和 21 字节 locator。
  - LOB / Long Row 读取不再依赖全文件扫描 LOB 页，而是从当前行 locator 出发按页链读取。
  - 数据页候选过滤进一步收紧，降低普通索引页、主键页被误识别为普通表数据页的概率。
  - DDL 类型格式化补充带时区时间、`INTERVAL`、`ROWID` 等类型处理。
  
  ### Fixed

  - 修复 `STORAGE(USING LONG ROW)` 表导出时多导出索引伪行的问题。
  - 修复部分 CLOB / Long Row 场景下 locator 无法正确追踪的问题。
  - 修复 `TIME` 类型误走 8 字节 `DATETIME` / `TIMESTAMP` 路径的问题。
  - 保留 `ALTER TABLE ADD COLUMN` 历史行恢复能力，同时避免普通索引页短行被误导出。
  
  ### Validation
  
  - `go test -count=1 ./...` 通过。
  - Windows x64 构建验证通过。
  - Linux x64 构建验证通过。
  - 真实样例 `JYC.T_LONG_ROW_LOB` 验证通过：
    - `rows exported: 5`
    - `rows failed: 0`
    - 没有额外索引伪行。
  - 回归样例 `JYC."t"` 验证通过：
    - 仍可导出 `ALTER TABLE ADD COLUMN` 前的历史行。
    - `id=10, name=NULL, birth=NULL` 未被误伤。
  
  ### Notes
  
  - Long Row / 行外 LOB 当前为初步支持。
  - 复杂 LOB 页链、损坏页、多版本残留页、特殊字符集和更多基础类型样例仍需继续验证。
  
  ## v0.2.1
  
  ### Fixed
  
  - 修复 `ALTER TABLE ADD COLUMN` 后历史行解析问题。
  - 旧记录会按历史列布局解析，新增尾部列在允许时补 `NULL`。
  - 修复主键页、索引页中前缀键值被误识别为普通表数据的问题。
  - 改进候选行去重逻辑，优先保留完整表行，降低重复短行、假行导出概率。
  
  ### Validation
  
  - `go test -count=1 ./...` 通过。
  - Windows x64 构建验证通过。
  - Linux x64 构建验证通过。
  - 真实样例验证：
    - `JYC."t"` 新旧行混合场景导出正常。
    - `rows failed: 0`。
  
  ------
  
  ## v0.2.0
  
  ### Added
  
  - 增强表数据定位能力，从“段范围扫描”升级为：
    - storage root
    - internal page refs
    - leaf chain
  - 新增表数据 page plan 定位逻辑。
  - 新增 DROP / TRUNCATE 后残留页恢复能力。
  - 新增交互式命令：
  
  ```text
  recover table <owner.table_name>;
  ```

  - `export-data` 新增恢复模式参数：

  ```text
  -recover
  ```
  
  - `bootstrap` 生成的 `tables.tsv` 新增恢复辅助字段：
    - `storage_id`
    - `root_file`
    - `root_page`
    - `assist_ids`
  
  ### Changed
  
  - 表数据导出优先使用 storage root / leaf chain 精确定位。
  - 段范围信息 `header_file/header_block/blocks` 作为辅助校验。
  - 当 root、leaf chain 或 page refs 不完整时，回退到段范围扫描或全文件扫描。
  - TRUNCATE 后恢复模式不再被当前缩小后的 `blocks/extents` 限制。
  
  ### Fixed
  
  - 进一步降低同名表、相似行格式、隐藏索引页导致误匹配的概率。
  - 改进相同 owner 或不同 owner 下相似表结构的数据页识别准确性。
  
  ### Validation

  - `go test ./...` 通过。
  - Windows x64 构建验证通过。
  - Linux x64 构建验证通过。
  - 真实样例验证：
    - `SY."t"` 导出正确。
    - `unload database` 导出成功，`rows failed: 0`。
  
  ------
  
  ## v0.1.9
  
  ### Added
  
  - 新增存储过程恢复。
  - 新增函数恢复。
  - 新增包和包体恢复。
  - `bootstrap` 生成 `routines.tsv`。
  - `load dictionary;` 支持加载 `routines.tsv`。
  - `unload table`、`unload user`、`unload database` 支持输出：
    - `CREATE OR REPLACE PROCEDURE`
    - `CREATE OR REPLACE FUNCTION`
    - `CREATE OR REPLACE PACKAGE`
    - `CREATE OR REPLACE PACKAGE BODY`
  
  ### Changed
  
  - `dmdul_dict` 对象字典范围扩大到存储过程、函数、包和包体。
  - `DATABASE_ddl.sql` 整库 DDL 输出范围进一步完善。
  
  ### Validation
  
  - `go test ./...` 通过。
  - 真实样例验证：
    - `routines loaded: 10`
    - `routines exported: 10`
  
  ------
  
  ## v0.1.8
  
  ### Added
  
  - 新增序列恢复能力。
  - 新增触发器恢复能力。
  - 新增对象级字典测试。
  - 完善 `dictionary_extras` 相关对象解析逻辑。
  
  ### Changed

  - 更新 README 和文档，补充序列、触发器和对象字典说明。
  - 改进 `dmdul_dict` 中扩展对象的加载和导出流程。
  
  ### Validation
  
  - `go test ./...` 通过。
  - Windows x64 构建验证通过。
  
  ------
  
  ## v0.1.7
  
  ### Added
  
  - 新增视图恢复能力。
  - 新增同义词恢复能力。
  - 新增表、视图、序列对象授权恢复能力。
  - 新增 `dictionary_extras.go`。
  - `bootstrap` 生成以下对象字典文件：
    - `views.tsv`
    - `synonyms.tsv`
    - `tab_privs.tsv`
  
  ### Changed
  
  - `unload database` 的 DDL 范围从用户、表、索引、约束扩展到更多数据库对象。
  - 更新 README、`docs/config.md`、`docs/usage.md`。
  
  ### Validation
  
  - `go test ./...` 通过。
  - Windows x64 构建验证通过。
  - Linux x64 构建验证通过。
  
  ------
  
  ## v0.1.6
  
  ### Added
  
  - 新增 `unload database;` 整库离线导出能力。
  - 新增字典驱动恢复流程。
  - `dmdul_dict` 开始真正参与 DDL 和数据导出。
  - `bootstrap` 增强 `tables.tsv` 段定位字段：
    - `header_file`
    - `header_block`
    - `bytes`
    - `blocks`
    - `extents`
  - 新增 dictionary override 和 segment 推断测试。
  
  ### Changed
  
  - DDL 和数据导出优先使用 `dmdul_dict` 中修正后的用户、表、字段、类型、表空间和存储组织信息。
  - `SYSTEM.DBF` 继续用于索引、约束、分区和物理定位等底层信息。
  - 整库导出可以同时生成：
    - `DATABASE_ddl.sql`
    - `DATABASE_data.sql`
  
  ### Fixed
  
  - 修复相同表名不同 owner 时可能误匹配数据页的问题。
  - 降低普通索引页被误识别为表数据页的概率。
  
  ### Validation
  
  - `go test ./...` 通过。
  - 真实样例验证：
    - `unload database;` 可导出整库 DDL 和数据。
    - `SY."t"` 不再串到 `SYSDBA.T`。
  
  ------
  
  ## v0.1.5
  
  ### Added
  
  - 新增数据字典持久化能力。
  - 新增 `dmdul_dict` 字典文件加载和保存逻辑。
  - 新增 `dul.log` 交互式操作日志。
  - `dul.log` 每条命令和错误记录带本地时间戳。
  - 新增字典文件相关测试。
  
  ### Changed
  
  - 交互式流程可以通过 `load dictionary;` 加载已保存的文本字典。
  - `list user`、`list table` 等命令可以展示字典来源和统计信息。
  
  ### Validation
  
  - `go test ./...` 通过。
  
  ------
  
  ## v0.1.4
  
  ### Changed
  
  - 重构 README 项目展示。
  - 增加项目定位、功能预览、安装构建、快速开始、安全提醒和版本规划。
  - 补充 GitHub 项目首页展示内容。
  
  ### Notes
  
  - 该版本主要为文档和项目展示优化版本。
  
  ------
  
  ## v0.1.3
  
  ### Added
  
  - 新增交互式 DUL Shell。
  - 新增 REPL 模式。
  - 新增 DUL 风格命令：
    - `bootstrap;`
    - `list user;`
    - `list table <owner>;`
    - `unload table <owner.table_name>;`
    - `unload user <owner>;`
  - 新增 `control.dul` 相关处理逻辑。
  - 新增字典模块初始实现。
  
  ### Changed
  
  - 改进 CLI 使用体验。
  - 操作方式从单纯命令行参数扩展为交互式恢复流程。
  
  ### Validation
  
  - `go test ./...` 通过。
  
  ------
  
  ## v0.1.2
  
  ### Changed
  
  - 调整部分命令参数。
  - 改进隐藏 `TABOBJ` / `INDEX` 内部对象识别。
  - 增强表号低位 `assist id` 处理。
  - 增加页级样例行确认候选表逻辑。
  
  ### Fixed
  
  - 降低普通索引页被误导出为表数据页的概率。
  - 改进离线数据页候选判断准确性。
  
  ### Validation
  
  - `go test ./...` 通过。
  
  ------
  
  ## v0.1.1
  
  ### Added
  
  - 新增 `.gitattributes`。
  - 规范 Go、Markdown、Shell、PowerShell 等文件换行符策略。
  
  ### Changed
  
  - 改进跨平台开发时的换行符一致性。
  
  ------
  
  ## v0.1.0
  
  ### Added
  
  - 初始版本发布。
  - 新增项目基础结构：
    - `cmd/dmdul`
    - `internal/cli`
    - `internal/dm`
    - `internal/storage`
    - `internal/version`
    - `docs`
    - `research`
  - 新增基础命令：
    - `inspect`
    - `inspect-ctl`
    - `scan-system`
    - `export-ddl`
    - `export-data`
    - `scan-partitions`
    - `version`
    - `help`
  - 初步支持：
    - `SYSTEM.DBF` 基础解析
    - `dm.ctl` 基础解析
    - 用户表 DDL 导出
    - 普通表数据导出
    - 分区表扫描研究命令
  
  ### Validation
  
  - `go test ./...` 通过。
