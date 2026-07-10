# dmdul

**Dameng Database Offline Recovery & Data Unloader**
**达梦数据库离线恢复与数据抽取工具**

`dmdul` 是一个使用 Go 编写的达梦数据库离线恢复工具。它面向数据库实例无法正常启动、常规恢复手段不可用、但仍能取得 `SYSTEM.DBF`、`dm.ctl` 和用户表空间 DBF 文件的场景，用于离线解析系统字典、恢复对象 DDL，并尽量从数据文件中抽取用户表数据。

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)
![License](https://img.shields.io/github/license/greatfinish/dmdul)
![Release](https://img.shields.io/github/v/release/greatfinish/dmdul)
![Stars](https://img.shields.io/github/stars/greatfinish/dmdul?style=social)

> `dmdul` 不是常规备份恢复工具，也不能替代 DMRMAN、归档恢复、闪回或专业数据恢复流程。它更适合作为极端故障场景下的最后手段。所有导出的 SQL 和数据都必须先在测试库验证。

------

## 核心能力

- 离线解析 `SYSTEM.DBF`、`dm.ctl` 和用户表空间 DBF 文件。
- 生成 `dmdul_dict` 文本字典，支持人工修正后再次参与恢复。
- 支持整库恢复命令：`unload database;`
- 支持单用户恢复：`unload user <owner>;`
- 支持单表恢复：`unload table <owner.table_name>;`
- 支持 DROP / TRUNCATE 后残留页扫描：`recover table <owner.table_name>;`
- 支持 Windows 和 Linux x86_64 发布包。
- 表数据定位优先使用 `storage root -> internal page refs -> leaf chain`，再回退到段范围扫描和全文件救援扫描。
- 支持 `STORAGE(USING LONG ROW)` 表的初步恢复。
- 支持 21 字节 LOB locator 解析。
- 支持从当前活动行出发追踪 `0x20` LOB 页和 `0x22` Long Row 页。
- 行 NULL metadata 优先使用显式 2-bit 解码，旧启发式解析作为兜底。
- 初步增强 `REAL`、`FLOAT`、`DOUBLE`、`TIME`、带时区时间、`INTERVAL`、`ROWID` 等基础类型解析。

------

## 适用场景

| 场景                   | 说明                                                         |
| ---------------------- | ------------------------------------------------------------ |
| 数据库无法 `open`      | 实例无法正常启动，但 DBF 文件仍可读取                        |
| 常规恢复失败           | 控制文件、ROLL、REDO、归档链路异常，DMRMAN 无法完成恢复      |
| 只剩数据文件           | 仍可尝试从 `SYSTEM.DBF` 和用户表空间文件恢复对象和数据       |
| 部分数据块损坏         | 大部分页仍可读时，可尝试按页扫描恢复                         |
| DROP / TRUNCATE 后救援 | 原数据块未被覆盖时，可尝试残留页恢复                         |
| 需要恢复 DDL           | 可离线导出用户、表、视图、序列、过程、函数、包、触发器、同义词、授权等对象 |

------

## 支持能力概览

| 能力                        | 状态       | 说明                                                         |
| --------------------------- | ---------- | ------------------------------------------------------------ |
| `SYSTEM.DBF` 基础解析       | ✅ 支持     | 页大小、簇大小、页数、字符集标记                             |
| `dm.ctl` 解析               | ✅ 支持     | 数据库名、表空间名、数据文件路径                             |
| `control.dul` 生成          | ✅ 支持     | 自动记录表空间号、文件号、表空间名和数据文件路径             |
| `dmdul_dict` 字典落盘       | ✅ 支持     | 生成可人工修正的 TSV 文本字典                                |
| 用户和角色授权              | ✅ 支持     | `CREATE USER`、`GRANT role TO user`                          |
| 表结构                      | ✅ 支持     | 字段、类型、默认值、临时表、堆表、树表                       |
| 索引和约束                  | ✅ 支持     | 主键、唯一、外键、CHECK、普通索引                            |
| 注释                        | ✅ 支持     | 表注释、字段注释                                             |
| 分区表 DDL                  | ✅ 初步支持 | RANGE / LIST / HASH 分区表                                   |
| 视图                        | ✅ 支持     | `CREATE OR REPLACE VIEW`                                     |
| 序列                        | ✅ 支持     | `CREATE SEQUENCE`                                            |
| 存储过程 / 函数 / 包 / 包体 | ✅ 支持     | `CREATE OR REPLACE PROCEDURE/FUNCTION/PACKAGE/PACKAGE BODY`  |
| 触发器                      | ✅ 支持     | `CREATE OR REPLACE TRIGGER`                                  |
| 同义词                      | ✅ 支持     | `CREATE OR REPLACE SYNONYM`                                  |
| 对象授权                    | ✅ 支持     | 表、视图、序列授权                                           |
| 表数据导出                  | ✅ 支持     | INSERT SQL 或 CSV                                            |
| 行内小 LOB                  | ✅ 初步支持 | 小 CLOB/TEXT、BLOB/IMAGE                                     |
| ALTER TABLE 后历史行        | ✅ 支持     | 新增尾列可补 `NULL`                                          |
| DROP / TRUNCATE 残留页恢复  | ✅ 初步支持 | 前提是原数据页未被覆盖                                       |
| 行外 LOB / Long Row         | ✅ 初步支持 | 支持 21 字节 locator，按当前行追踪 `0x20` LOB 页和 `0x22` Long Row 页 |
| 行 NULL metadata            | ✅ 支持     | 优先使用显式 2-bit metadata 解码，失败后回退旧启发式路径     |
| 基础类型解析                | ✅ 增强     | 初步支持 REAL/FLOAT/DOUBLE、TIME、带时区时间、INTERVAL、ROWID |



------

## 下载

请从 [Releases](https://github.com/greatfinish/dmdul/releases) 下载最新版本。

| 平台        | 包名                                 |
| ----------- | ------------------------------------ |
| Windows x64 | `dmdul_windows_amd64_<version>.zip`  |
| Linux x64   | `dmdul_linux_amd64_<version>.tar.gz` |

下载后建议校验 Release 页面提供的 SHA256。

Windows：

```powershell
Get-FileHash .\dmdul_windows_amd64_<version>.zip -Algorithm SHA256
```

Linux：

```bash
sha256sum dmdul_linux_amd64_<version>.tar.gz
```

查看版本：

```bash
./dmdul version
```

或 Windows：

```powershell
.\dmdul.exe version
```

------

## 快速开始

### 1. 准备离线文件

建议把相关文件放在同一个目录中：

```text
D:\temp\oldpro\
├── SYSTEM.DBF
├── dm.ctl
├── MAIN.DBF
├── ROLL.DBF
├── TEMP.DBF
└── TBS_*.DBF
```

`dm.ctl` 是可选增强文件，但强烈建议提供。没有 `dm.ctl` 时，工具会尝试通过 `control.dul` 和 DBF 页头识别数据文件。

------

### 2. 启动交互式 DUL Shell

Windows：

```powershell
.\dmdul.exe
```

Linux：

```bash
./dmdul
```

示例：

```text
DMDUL> set system D:\temp\oldpro\SYSTEM.DBF;
DMDUL> set data_dir D:\temp\oldpro;
DMDUL> bootstrap;
DMDUL> list user;
DMDUL> list table HR_TEST;
DMDUL> unload database;
DMDUL> exit;
```

`bootstrap;` 会生成：

```text
control.dul
init.dul
dul.log
dmdul_dict/
```

`unload database;` 默认生成：

```text
DATABASE_ddl.sql
DATABASE_data.sql
```

------

## 推荐恢复流程

```text
准备 SYSTEM.DBF、dm.ctl、用户表空间 DBF
        |
        v
启动 dmdul
        |
        v
set system / set data_dir
        |
        v
bootstrap
        |
        v
检查 dmdul_dict
        |
        v
必要时人工修正 users.tsv / tables.tsv / columns.tsv / routines.tsv 等
        |
        v
load dictionary
        |
        v
unload database
        |
        v
审核 DDL 和数据 SQL
        |
        v
导入测试库验证
```

详细流程见：[离线恢复流程](https://github.com/greatfinish/dmdul/blob/main/docs/recovery-workflow.md)。

------

## dmdul_dict 字典目录

`bootstrap;` 会在输出目录生成 `dmdul_dict`。这些 TSV 文件可以人工修正，修正后执行：

```text
DMDUL> load dictionary;
```

后续 `unload table`、`unload user`、`unload database` 会优先使用文本字典中的修正结果。

| 文件            | 说明                                       |
| --------------- | ------------------------------------------ |
| `meta.tsv`      | SYSTEM.DBF、页大小、字符集、对象数量等摘要 |
| `users.tsv`     | 用户 / owner 列表                          |
| `tables.tsv`    | 表摘要、表空间、段信息、storage 信息       |
| `columns.tsv`   | 字段定义、字段类型、长度、默认值、nullable |
| `views.tsv`     | 视图定义                                   |
| `sequences.tsv` | 序列定义                                   |
| `routines.tsv`  | 存储过程、函数、包、包体源码               |
| `triggers.tsv`  | 触发器定义                                 |
| `synonyms.tsv`  | 同义词定义                                 |
| `tab_privs.tsv` | 表、视图、序列等对象授权                   |

`tables.tsv` 中的重要恢复字段：

| 字段           | 说明                       |
| -------------- | -------------------------- |
| `header_file`  | 段头文件号                 |
| `header_block` | 段头块号                   |
| `bytes`        | 段大小                     |
| `blocks`       | 段块数                     |
| `extents`      | extent 数量                |
| `storage_id`   | 主数据 storage / assist id |
| `root_file`    | storage root 文件号        |
| `root_page`    | storage root 页号          |
| `assist_ids`   | 辅助 storage id 列表       |

------

## 表数据定位策略

`dmdul` 当前采用分层定位策略：

```text
优先级 1：storage root / internal refs / leaf chain
优先级 2：header_file / header_block / blocks 段范围校验
优先级 3：段范围扫描
优先级 4：全文件残留页扫描
```

正常表数据导出时，优先通过 storage root 和 leaf chain 精确定位数据页，减少同名表、相似行格式、索引页误识别带来的误导出。

当 root 损坏、leaf 链断裂或 TRUNCATE / DROP 后当前字典范围已经变化时，可以使用恢复扫描模式进行兜底救援。

------

## DROP / TRUNCATE 残留页恢复

如果表被 `TRUNCATE` 或 `DROP` 后，原数据块尚未被新写入覆盖，可以尝试：

```text
DMDUL> recover table USERS1.T_TEST;
```

也可以指定输出前缀：

```text
DMDUL> recover table USERS1.T_TEST to users1_t_test_recover;
```

DROP 场景中，当前 `SYSTEM.DBF` 里可能已经没有表定义。此时需要：

1. 加载 DROP 前保存的 `dmdul_dict`；
2. 或人工在 `tables.tsv`、`columns.tsv` 中补齐表结构；
3. 必要时补充 `storage_id`、`root_file`、`root_page`、`assist_ids` 等恢复辅助字段。

------

## 常用命令

### 交互式命令

```text
bootstrap;
load init;
load dictionary;
show parameter;
list user;
list table <owner>;
unload table <owner.table_name>;
unload user <owner>;
unload database;
recover table <owner.table_name>;
set data_format sql;
set data_format csv;
exit;
```

功能性命令行子命令已经移除。请直接运行 `dmdul` 进入交互界面；`help` 和
`version` 仅用于查看帮助与版本，不执行数据库恢复操作。

------

## 从源码构建

### 环境要求

- Go 1.22+
- Windows / Linux / macOS

克隆并测试：

```bash
git clone https://github.com/greatfinish/dmdul.git
cd dmdul
go test ./...
```

Windows 构建：

```powershell
$ver = "v0.2.1"
$commit = git rev-parse --short HEAD

go build `
  -ldflags "-X dmdul/internal/version.Version=$ver -X dmdul/internal/version.Commit=$commit" `
  -o bin\dmdul.exe `
  ./cmd/dmdul
```

Linux x64 交叉编译：

```powershell
$env:CGO_ENABLED="0"
$env:GOOS="linux"
$env:GOARCH="amd64"

go build `
  -ldflags "-s -w -X dmdul/internal/version.Version=$ver -X dmdul/internal/version.Commit=$commit" `
  -o bin\dmdul_linux_amd64 `
  ./cmd/dmdul

Remove-Item Env:\GOOS
Remove-Item Env:\GOARCH
Remove-Item Env:\CGO_ENABLED
```

Linux 本机编译：

```bash
go test ./...
go build -ldflags "-s -w" -o bin/dmdul ./cmd/dmdul
```

------

## 文档

- [安装方式](https://github.com/greatfinish/dmdul/blob/main/docs/install.md)
- [使用示例](https://github.com/greatfinish/dmdul/blob/main/docs/usage.md)
- [配置和参数说明](https://github.com/greatfinish/dmdul/blob/main/docs/config.md)
- [离线恢复流程](https://github.com/greatfinish/dmdul/blob/main/docs/recovery-workflow.md)
- [本地开发、测试、构建说明](https://github.com/greatfinish/dmdul/blob/main/docs/development.md)
- [版本变更记录](https://github.com/greatfinish/dmdul/blob/main/CHANGELOG.md)
- [逆向扫描笔记](https://github.com/greatfinish/dmdul/blob/main/docs/offline-system-scan.md)
- [系统字典字段笔记](https://github.com/greatfinish/dmdul/blob/main/docs/system-dictionary-fields.md)

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

请不要把生产库文件、导出结果或敏感数据提交到公开仓库：

```text
*.DBF
*.dbf
dm.ctl
dm.ini
init.dul
control.dul
dmdul_dict/
dul.log
*.sql
*.csv
真实生产数据
导出的业务数据
```

建议在隔离目录中放置待恢复文件，并把导出的 SQL、CSV、日志都按敏感数据处理。

------

## 当前限制

- 工具只读取离线文件，不会修改原始 DBF 文件。
- 离线恢复结果受达梦版本、页大小、字符集、表类型、行格式和损坏程度影响。
- 导出的 SQL 必须人工审核，并先导入测试库验证。
- DROP / TRUNCATE 残留页恢复依赖原数据页是否被覆盖，不能保证一定成功。
- 行外大 LOB、迁移行、链式行、复杂损坏页仍在持续验证。
- 不保证恢复结果与故障前数据库在事务一致性层面完全一致。
- Long Row / 行外 LOB 当前为初步支持，复杂 LOB 页链、损坏页、多版本残留页、特殊字符集和更多数据类型样例仍需继续验证。

------

## 版本路线

| 版本   | 方向                                           |
| ------ | ---------------------------------------------- |
| v0.2.x | 稳定整库恢复、增强数据页定位、修复真实样例问题 |
| v0.3.x | 增强数据导出能力，继续完善 LOB、迁移行、链式行 |
| v0.4.x | 增强达梦系统字典解析和更多对象类型恢复         |
| v1.0.0 | 功能稳定后发布正式版本                         |

------

## 贡献

欢迎提交 Issue、测试样例、失败案例和改进建议。

提交 Pull Request 前建议执行：

```bash
go test ./...
```

如果涉及数据导出逻辑，请尽量补充最小化测试样例。

------

## 开源协议

本项目使用 [MIT License](https://chatgpt.com/c/LICENSE)。
