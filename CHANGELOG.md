# 版本变更记录

本项目遵循“尽量记录可验证能力”的原则。版本号暂不严格承诺语义化兼容，直到离线解析能力稳定。

## 0.1.0-dev

当前开发版本。

### 已支持

- 解析 `SYSTEM.DBF` 基础信息：
  - 簇大小。
  - 页大小。
  - 页数。
  - 字符集标记。
- 解析 `dm.ctl`：
  - 数据库名。
  - 控制项。
  - 表空间名称。
  - 数据文件路径。
- 导出 DDL：
  - 普通用户 `CREATE USER`。
  - 用户角色授权 `GRANT role TO user`。
  - 用户表。
  - 字段、类型、长度、精度、默认值。
  - 堆表 `NOBRANCH` 和树表 `CLUSTERBTR`。
  - 全局临时表。
  - 索引、主键、唯一约束、外键、CHECK 约束。
  - 表注释、字段注释。
  - RANGE/LIST/HASH 分区表 DDL。
- 导出数据：
  - 普通 CLUSTERBTR 树表。
  - 普通 NOBRANCH 堆表。
  - 页尾 slot array 行定位。
  - big-endian 行长。
  - 常见整数、`NUMBER`/`DECIMAL`。
  - 3 字节 `DATE`。
  - 8 字节 `DATETIME`/`TIMESTAMP`。
  - 短 varchar、扩展短 varchar、两字节长度长 varchar。
  - 行内小 `CLOB`/`TEXT` 和行内小 `BLOB`/`IMAGE`；二进制值导出为 `HEXTORAW('...')`。
  - 末尾可空变长字段省略为 NULL 的已观察格式。
- 命令：
  - `inspect`
  - `inspect-ctl`
  - `scan-system`
  - `export-ddl`
  - `export-data`
  - `scan-partitions`

### 已知限制

- 对象权限授权仍需要继续校验 `SYSGRANTS.PRIVID` 到权限名称的映射。
- 行外大 LOB、链式行、迁移行仍在验证中。
- 严重损坏页的容错扫描能力还需要增强。
- 部分达梦版本、页大小、初始化参数组合仍需要更多样本验证。
- 当前输出以 SQL 为主，暂未提供 CSV/JSON 数据导出。

### 文档

- 新增中文 README。
- 新增安装、使用、配置、开发文档。
- 整理离线扫描规则和系统字典字段笔记。

## v0.1.6

### Added

- 新增 `unload database;` 整库导出。
- 新增字典驱动恢复流程。
- 新增 `dictionary_overrides.go`。
- 新增 `dictionary_segments.go`。
- `bootstrap` 可生成 `header_file/header_block/bytes/blocks/extents` 段信息。

### Fixed

- 修复同名表不同 owner 时可能误匹配数据页的问题。
- 降低普通索引页被误识别为表数据页的概率。

### Tests

- 新增 unload database 自动化测试。
- 新增 dictionary override 和 segment 推断测试。
