# Changelog

所有重要变更都会记录在这里。

版本格式遵循：

```text
v主版本.次版本.修订版本
```

当前项目仍处于早期可用和持续验证阶段，离线恢复结果受达梦版本、页大小、字符集、表类型、行格式、数据页损坏程度等因素影响。

------

## Unreleased

### Changed

- 表数据正常导出改为真正执行 `storage root -> internal refs -> leaf chain` page plan，并通过
  `ReadAt` 只读取计划页；不再先全面扫描数据文件后再用计划过滤。
- page plan 不完整或计划页校验失败时，按“同 group `storage_id` 扫描 -> 段范围读取”分层回退；
  全文件残留页扫描仅用于 `recover table`。
- `tables.tsv` 中的 `storage_id/root_file/root_page` 现在会直接参与数据页计划生成。
- `unload` / `recover` 控制台与 `dul.log` 新增 `planned pages`、`direct pages read`、
  `fallback pages scanned` 和 `fallback reason` 诊断。
- leaf 链断裂、循环或页身份/storage id 不一致时不再接受部分 page plan。

### Validation

- 新增“计划成功时直接读取页数等于计划页数”回归测试。
- 新增断链、同 group storage fallback、segment fallback 和 recover 全文件扫描测试。
- `go test -count=1 ./...` 与 `go vet ./...` 通过。

## v0.4.1 - Standard Bootstrap & Native DMP Export

Release theme: complete the offline chain from standard SYSTEM bootstrap and
dictionary recovery to SQL/CSV/DMP data output and official `dimp` loading.

### Added

- Added Dameng native-compatible pure-data DMP export format.
- Added `data_format=dmp` for interactive unload workflows.
- Added table-, user-, and database-level DMP export; user/database exports create one DMP per non-empty table.
- Added DMP large-table support:
  - 64-bit long-field size encoding
  - multi-phase output
  - dump paths larger than 4 GiB
- Added streaming out-of-line CLOB/BLOB locator export.
- Added `STORAGE(USING LONG ROW)` data path support.
- Added RANGE/LIST/HASH partition-table DMP compatibility with `dimp`; rows are exported through the parent table and routed by DM during import.
- Added UTF-8, GB18030, and EUC-KR DMP headers, including page size, extent size, `UNICODE_FLAG`, and `CASE_SENSITIVE` metadata.
- Added standard two-stage bootstrap through page-0 anchors, storage roots, internal page references, and leaf chains.
- Added complete regular-column type recovery for SQL/CSV/DMP paths, including 13 INTERVAL
  qualifiers, 9-digit timestamps, timezone types, ROWID, BFILE, JSON/JSONB, national character
  variants, and DM-compatible aliases.
- Added `docs/data-types.md` with the official-document comparison, catalog normalization rules,
  output-format limits, and the 2026-07-13 DM8 validation matrix.
- Bootstrap now automatically detects database parameters from offline files.
- Added persistent parameter metadata in init.dul.
- Added interactive commands:

  - `show parameter;`
  - `load parameter;`

- Bootstrap now records parameter source information.

Supported parameters:

- PAGE_SIZE
- EXTENT_SIZE
- PAGE_COUNT
- UNICODE_FLAG / CHARSET
- CASE_SENSITIVE
- DB_NAME
- INSTANCE_NAME

### Improved

- Grouped default `unload` and `recover` artifacts under a dedicated `output/` directory beside `dmdul_dict`; explicit `output_dir` still overrides the destination.
- Kept `init.dul`, `control.dul`, `dul.log`, and `dmdul_dict` in the working/data directory so large user/database exports no longer clutter it.
- Avoid using stale init.dul charset/case settings when switching databases.
- Database metadata is now associated with the current SYSTEM.DBF.
- Parameter loading can restore previous bootstrap environment.
- DDL formatting now preserves lengths for `CHARACTER VARYING`, `NATIONAL CHARACTER`,
  `NATIONAL CHARACTER VARYING`, and `NCHAR VARYING` dictionary types.

### Fixed

- Fixed `BINARY(n)` row decoding: DM stores it as a fixed-width field, not a length-prefixed value.
- Fixed the negative NUMBER base-100 exponent, which shifted some negative decimals by two digits.
- Fixed ROWID formatting to decode the physical 12-byte `epno/partno/real_rowid` layout and emit
  the official 18-character value.
- Fixed generated `CONS<number>` constraint names: recovered DDL now lets DM assign a new name,
  because those internal names cannot be reused in `ALTER TABLE ADD CONSTRAINT`.
- Documented and validated the DM8 JSON/JSONB DMP limitation: use `FAST_LOAD=N`; both dmdul and
  official `dexp` files can become unqueryable when imported with `FAST_LOAD=Y`.

### Parameter Detection

Detection priority:

| Parameter      | Source                   |
| -------------- | ------------------------ |
| PAGE_SIZE      | SYSTEM.DBF + 0x84        |
| EXTENT_SIZE    | SYSTEM.DBF + 0x80        |
| PAGE_COUNT     | SYSTEM.DBF + 0x8C        |
| CHARSET        | SYSTEM.DBF page 4 + 0x2D |
| CASE_SENSITIVE | SYSTEM.DBF page 4 + 0x2C |
| INSTANCE_NAME  | SYS.SYSOPENHISTORY       |
| DB_NAME        | dm.ctl                   |

Default values:

| Parameter      | Default  |
| -------------- | -------- |
| PAGE_SIZE      | 8192     |
| EXTENT_SIZE    | 16       |
| CHARSET        | GB18030  |
| CASE_SENSITIVE | 1        |
| DB_NAME        | DAMENG   |
| INSTANCE_NAME  | DMSERVER |

### Validation

Verified with:

- oldpro
- FEIGE
- QYT

Validation includes:

- bootstrap
- load parameter
- show parameter
- unload database

Native DMP validation also includes:

- official `dimp DATA_ONLY=Y FAST_LOAD=Y` loading
- UTF-8, GB18030, and EUC-KR headers
- RANGE/LIST/HASH partition tables
- 32 MiB out-of-line LOB streaming
- multi-phase output and 64-bit size regression coverage
- official `dimp` loading of the complete regular-type matrix
- 15 JSONB scalar/container samples with `FAST_LOAD=N`
- recovered SQL DDL and data replay in an isolated schema

## v0.4.0

### Added

- 新增 DM8 DMP 格式解析与纯数据流式写入能力。
- DMP 头支持 UTF-8、GB18030、EUC-KR 的 encoding code 与 UNICODE_FLAG 配对。
- DMP 写入器支持 8 MiB 多 phase 自动切分和 Reader 流式长字段，已用分区表及 32 MiB LOB 完成官方 `dimp/FAST_LOAD` 回灌。
- DMP 行数解析支持累计 phase-2 以后各 phase，并识别 `0xFFFFFFFF` 行续传标记。
- `set data_format dmp;` 正式接入 `unload table`、`unload user` 和 `unload database`。
- 用户级和整库级 DMP 按表生成，空表不生成空 DMP，便于逐表重试和并行快速装载。
- 行外 CLOB/BLOB 在 DMP 导出路径中按 locator 页链流式读取，不再先拼接完整 LOB 到内存。
- 用户数据页扫描和 LOB 页缓存统一恢复 4 KiB 扇区边界的页保护字节，避免 16K/32K 页中的字段内容被保护字节污染。
- DMP 写入器支持暂停/继续，整库存在大量表时最多只保持有限数量的活动文件句柄。
- 新增 `case_sensitive=auto|0|1`；`auto` 从 `SYSTEM.DBF` 第 4 页偏移 `0x2C` 读取建库大小写敏感标志，也允许控制页损坏时显式修正 DMP 文件头。
- `bootstrap` 日志和 `dmdul_dict/meta.tsv` 记录已解析的 `case_sensitive` 值及物理来源；该偏移已通过 6 个已有实例和一组同字符集差分实例验证。
- 补充 BIT、BYTE、BINARY、`INTERVAL YEAR TO MONTH` 和 DM ROWID 显示值解析。
- 增加超过 4 GiB 长字段长度的 64 位编码回归测试，整表通过多 phase 持续输出。
- 新增 `docs/dmp-format-research.md`，记录 DMP 头、phase、字段、footer、压缩和官方回灌验证结论。
- 新增 `unload object <owner|all>;`，用于单独导出 owner 或整库对象字典 DDL，不导出表数据。
- DDL 和数据导出器新增单次扫描、按表多文件分流能力。
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

- `unload user <owner>;` 改为按表生成 `<prefix>_<table>_ddl.sql` 和 `<prefix>_<table>_data.sql|csv|dmp`。
- `unload user` 不再生成混合了所有模式对象的用户级 DDL；完整对象定义改由 `unload object` 负责。
- 单表 DDL 不再夹带同一 owner 的无关视图、序列、函数、包或同义词。
- CSV 格式下空表只生成 DDL，不生成空 CSV。
- DMP 格式下空表只生成 DDL，不生成空 DMP。
- 移除重复语义的 `unload user all;`；整库恢复统一使用 `unload database;`。
- DMP 研究确认 TIME 小数秒无法由当前版本官方 DMP 通道无损保存，并确认跨数据库字符集导入不会可靠自动转码。
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
- 表级、用户级、整库级 DMP 自动化测试通过，覆盖空表跳过、输出前缀、MD5/头校验和表行数。
- 300 页 LOB 链流式读取测试通过，页缓存保持固定上限。
- oldpro 真实离线样例通过官方 `dimp DATA_ONLY=Y FAST_LOAD=Y` 回灌：LOB 表 10 行、RANGE 分区表 10 行，导入无告警。

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
