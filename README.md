# dmdul

**达梦数据库离线抽取工具 / Offline Dameng Database Extraction Helper**

用于数据库实例无法正常启动时，基于 `SYSTEM.DBF`、`dm.ctl` 和用户表空间 DBF 文件，离线解析系统字典、导出建表 DDL，并尽量从数据文件中抽取用户表数据。

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)
![License](https://img.shields.io/github/license/greatfinish/dmdul)
![Release](https://img.shields.io/github/v/release/greatfinish/dmdul)
![Stars](https://img.shields.io/github/stars/greatfinish/dmdul?style=social)

------

## 项目定位

`dmdul` 是一个使用 Go 编写的达梦数据库离线救援工具，目标是在数据库实例无法正常 `open`、常规恢复手段不可用、但仍能取得 DBF 文件的情况下，尽量从离线数据文件中恢复元数据和业务数据。

它不是常规备份恢复工具，也不能替代 DMRMAN、归档日志恢复、数据库闪回或专业数据恢复流程。它更适合在极端故障场景下作为最后手段，用于辅助恢复 DDL 和部分用户表数据。

> 当前项目仍处于逆向验证和早期可用阶段。请先在测试环境验证导出的 SQL 和数据，再用于正式恢复流程。离线抽取结果可能存在逻辑不一致，不能保证 100% 成功。

------

## 适用场景

常见适用场景：

| 场景               | 说明                                           |
| ------------------ | ---------------------------------------------- |
| 数据库无法 `open`  | 实例无法正常启动，但数据文件仍可读取           |
| SYSTEM 表空间仍在  | 可以尝试解析系统字典和对象元数据               |
| 控制文件或日志异常 | `dm.ctl`、REDO、ROLL 等异常导致常规恢复失败    |
| 只剩 DBF 文件      | 需要尽量从数据文件中抽取业务表数据             |
| 部分数据块损坏     | 大部分数据页仍可读时，可尝试按页扫描恢复       |
| 需要恢复 DDL       | 离线生成用户、表、字段、索引、约束、注释等 DDL |

------

## 功能预览

| 能力                  | 状态       | 说明                                 |
| --------------------- | ---------- | ------------------------------------ |
| `SYSTEM.DBF` 基础解析 | ✅ 支持     | 识别页大小、簇大小、页数、字符集标记 |
| `dm.ctl` 解析         | ✅ 支持     | 识别数据库名、表空间名、数据文件路径 |
| 用户 DDL 导出         | ✅ 支持     | 用户、表、字段、索引、约束、注释     |
| 普通表数据导出        | ✅ 支持     | 导出为 INSERT SQL 或 CSV             |
| 字符集处理            | ✅ 支持     | UTF-8、GB18030/GBK、EUC-KR           |
| 交互式 DUL 模式       | ✅ 支持     | 提供类似 Oracle DUL 的交互式命令     |
| 分区表识别            | ✅ 初步支持 | RANGE / LIST / HASH 分区表 DDL       |
| 行内小 LOB            | ✅ 初步支持 | 小 CLOB/TEXT、BLOB/IMAGE 行内值      |
| 行外大 LOB            | 🚧 验证中   | 后续版本继续增强                     |
| 迁移行 / 链式行       | 🚧 验证中   | 复杂场景仍需继续验证                 |

------

## 当前能力

### 离线字典解析

- 读取 `SYSTEM.DBF` 基础信息：
  - 页大小
  - 簇大小
  - 页数
  - 字符集标记
- 可选解析 `dm.ctl`：
  - 数据库名
  - 表空间名
  - 数据文件路径
- 扫描 `data_dir` 下 DBF 页头，辅助生成 `control.dul`。
- `bootstrap` 会把字典摘要写入 `dmdul_dict` 文本目录，便于再次启动快速加载和人工修正。
- `bootstrap` 会把识别到的数据库字符集回写到 `init.dul` 的 `charset` 参数。

### DDL 导出

支持离线导出用户表 DDL：

- 普通用户 `CREATE USER`
- 角色授权 `GRANT role TO user`
- 表结构
- 字段名、字段类型、默认值
- 索引
- 主键
- 唯一约束
- 外键
- CHECK 约束
- 表注释
- 字段注释
- 临时表
- 堆表 / NOBRANCH
- 树表 / CLUSTERBTR
- RANGE / LIST / HASH 分区表建表语句

### 数据导出

支持离线导出普通用户表数据：

- 输出为 `INSERT INTO`
- 输出为 CSV
- 支持常见整数类型
- 支持 `NUMBER` / `DECIMAL`
- 支持 `DATE`
- 支持 `DATETIME` / `TIMESTAMP`
- 支持 `VARCHAR` / `VARCHAR2`
- 支持行内小 `CLOB` / `TEXT`
- 支持行内小 `BLOB` / `IMAGE`
- `BLOB` 导出为 `HEXTORAW('...')`
- 支持 CLUSTERBTR 树表和 NOBRANCH 堆表的普通行

### 交互式命令

提供类似 Oracle DUL 的交互式命令行：

```text
bootstrap;
load dictionary;
list user;
list table <owner>;
unload table <owner.table_name>;
unload user <owner>;
exit;
```

### 研究辅助命令

提供以下辅助命令：

```text
inspect
inspect-ctl
scan-system
scan-partitions
export-ddl
export-data
version
help
```

------

## 安装与构建

### 环境要求

- Go 1.22+
- Windows / Linux / macOS
- 可读取的达梦 DBF 文件
- 可选：`dm.ctl`

### 从源码构建

```powershell
git clone https://github.com/greatfinish/dmdul.git
cd dmdul

go test ./...
go build -o .\bin\dmdul.exe .\cmd\dmdul
```

Linux / macOS：

```bash
git clone https://github.com/greatfinish/dmdul.git
cd dmdul

go test ./...
go build -o ./bin/dmdul ./cmd/dmdul
```

### 查看帮助

Windows：

```powershell
.\bin\dmdul.exe --help
```

Linux / macOS：

```bash
./bin/dmdul --help
```

------

## 快速开始

### 方式一：交互式模式

启动交互式 DMDUL：

```powershell
.\bin\dmdul.exe
```

交互式抽取示例：

```text
DMDUL> set system D:\temp\oldpro\SYSTEM.DBF;
DMDUL> set data_dir D:\temp\oldpro;
DMDUL> bootstrap;
DMDUL> list user;
DMDUL> list table HR_TEST;
DMDUL> unload table HR_TEST.EMP_INFO;
DMDUL> set data_format csv;
DMDUL> unload user HR_TEST;
DMDUL> exit;
```

说明：

- `dm.ctl` 是可选增强文件。
- 未显式指定 `dm.ctl` 时，如果 `SYSTEM.DBF` 同目录存在 `dm.ctl`，工具会自动尝试使用。
- `bootstrap` 会扫描 `data_dir` 下的 DBF 页头，并生成 `control.dul`。
- `control.dul` 用于记录表空间号、文件号、表空间名和数据文件路径。
- `bootstrap` 会生成 `dmdul_dict\meta.tsv`、`users.tsv`、`tables.tsv`、`columns.tsv`。
- 再次启动后可以用 `load dictionary;` 从文本字典恢复，`list user;` 也会显示字典来源和统计。
- 交互模式会自动生成 `init.dul` 参数文件；未设置 `data_dir` 时写在当前目录，
  设置 `data_dir` 后写到 `data_dir` 目录。
- `init.dul` 可以人工修改，交互界面中执行 `load init;` 可重新加载参数。
- `unload table` 用于抽取单表。
- `unload user` 用于抽取指定用户下的表。
- 数据默认导出为 INSERT SQL。
- 可通过 `set data_format csv;` 导出 CSV；空表不会生成空 CSV 文件。

### 方式二：命令行直接导出 DDL

```powershell
.\bin\dmdul.exe export-ddl `
  -file D:\temp\oldpro\SYSTEM.DBF `
  -ctl D:\temp\oldpro\dm.ctl `
  -out D:\temp\oldpro\dm_offline_schema.sql
```

指定用户和字符集：

```powershell
.\bin\dmdul.exe export-ddl `
  -file D:\temp\oldpro\SYSTEM.DBF `
  -ctl D:\temp\oldpro\dm.ctl `
  -out D:\temp\oldpro\schema.sql `
  -owner HR_TEST,SYSDBA `
  -charset gb18030
```

### 方式三：命令行直接导出数据

```powershell
.\bin\dmdul.exe export-data `
  -file D:\temp\oldpro\SYSTEM.DBF `
  -ctl D:\temp\oldpro\dm.ctl `
  -data-dir D:\temp\oldpro `
  -out D:\temp\oldpro\dm_offline_data.sql
```

指定表：

```powershell
.\bin\dmdul.exe export-data `
  -file D:\temp\oldpro\SYSTEM.DBF `
  -ctl D:\temp\oldpro\dm.ctl `
  -table SYSDBA.BIN_TEST2,SYSDBA.BIN_TEST2_CHILD
```

------

## 常用命令

```text
dmdul inspect -file SYSTEM.DBF
dmdul inspect -file MAIN.DBF -sample 256
dmdul scan-system -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl
dmdul inspect-ctl -ctl oldpro\dm.ctl
dmdul export-ddl
dmdul export-ddl -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl -out oldpro\dm_offline_schema.sql
dmdul export-ddl -file SYSTEM.DBF -ctl dm.ctl -out schema.sql -owner HR_TEST,SYSDBA -charset gb18030
dmdul export-data -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl -data-dir oldpro -out oldpro\dm_offline_data.sql
dmdul export-data -file SYSTEM.DBF -ctl dm.ctl -table SYSDBA.BIN_TEST2,SYSDBA.BIN_TEST2_CHILD
dmdul scan-partitions -file SYSTEM.DBF -ctl dm.ctl -owner all
```

------

## 文档

- [安装方式](https://chatgpt.com/c/docs/install.md)
- [使用示例](https://chatgpt.com/c/docs/usage.md)
- [配置和参数说明](https://chatgpt.com/c/docs/config.md)
- [本地开发、测试、构建说明](https://chatgpt.com/c/docs/development.md)
- [版本变更记录](https://chatgpt.com/c/CHANGELOG.md)
- [逆向扫描笔记](https://chatgpt.com/c/docs/offline-system-scan.md)
- [系统字典字段笔记](https://chatgpt.com/c/docs/system-dictionary-fields.md)

------

## 项目目录

```text
cmd/dmdul/          CLI 入口
internal/cli/       命令行参数、交互式 REPL 和输出
internal/dm/        达梦 SYSTEM.DBF、dm.ctl、DDL、数据行解析
internal/storage/   文件检查和二进制采样
internal/version/   版本信息
docs/               用户文档和研究笔记
research/           临时研究脚本和实验记录
```

------

## 安全提醒

请不要把以下文件提交到公开仓库：

```text
*.DBF
dm.ctl
dm.ini
*.log
*.sql
*.csv
dmdul_dict/
真实生产数据
导出的业务数据
```

建议在 `.gitignore` 中明确排除：

```gitignore
*.DBF
*.dbf
dm.ctl
dm.ini
*.log
*.sql
*.csv
dmdul_dict/
bin/
dist/
tmp/
```

------

## 限制说明

当前版本仍存在以下限制：

- 只读取离线文件，不会修改数据库文件。
- 离线恢复结果受达梦版本、页大小、字符集、表类型、行格式影响。
- 导出的 SQL 必须人工审核后再导入目标库。
- 行外大 LOB、迁移行、链式行、复杂损坏页等场景仍需要继续验证。
- 严重损坏的数据文件可能只能恢复部分对象或部分数据。
- 不保证恢复结果与故障前数据库在事务层面完全一致。

------

## 版本规划

| 版本   | 方向                         |
| ------ | ---------------------------- |
| v0.1.x | 修复问题、完善文档、增强测试 |
| v0.2.x | 增强 DDL 导出能力            |
| v0.3.x | 增强数据导出能力             |
| v0.4.x | 增强达梦系统字典解析         |
| v1.0.0 | 功能稳定后发布正式版本       |

------

## 贡献

欢迎提交 Issue、测试样例和改进建议。

如果你要提交 Pull Request，建议先执行：

```powershell
go test ./...
go build -o .\bin\dmdul.exe .\cmd\dmdul
```

------

## 开源协议

本项目使用 [MIT License](https://chatgpt.com/c/LICENSE)。
