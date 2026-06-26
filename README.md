# dmdul

`dmdul` 是一个使用 Go 编写的达梦数据库离线抽取工具。它面向数据库实例无法正常启动、但还能拿到 `SYSTEM.DBF`、`dm.ctl` 和用户表空间 DBF 文件的场景，离线解析系统字典、生成建表 DDL，并尽量从数据文件中恢复用户表数据。

> 当前项目仍处于逆向验证和早期可用阶段。请先在测试环境验证导出的 SQL，再用于正式恢复流程。

## 当前能力

- 读取 `SYSTEM.DBF` 基础信息：页大小、簇大小、页数、字符集标记。
- 解析 `dm.ctl` 中的数据库名、表空间名和数据文件路径。
- 离线导出用户表 DDL：
  - 普通用户 `CREATE USER` 和角色授权 `GRANT role TO user`。
  - 表、字段、字段类型、默认值。
  - 索引、主键、唯一约束、外键、CHECK 约束。
  - 表注释、字段注释。
  - 临时表、堆表/NOBRANCH、树表/CLUSTERBTR。
  - RANGE/LIST/HASH 分区表的建表语句。
- 离线导出普通用户表数据为 `INSERT INTO`：
  - 支持常见整数、`NUMBER`/`DECIMAL`、`DATE`、`DATETIME`/`TIMESTAMP`、`VARCHAR`/`VARCHAR2`。
  - 支持行内小 `CLOB`/`TEXT` 文本和行内小 `BLOB`/`IMAGE` 二进制值；`BLOB` 导出为 `HEXTORAW('...')`。
  - 支持 CLUSTERBTR 树表和 NOBRANCH 堆表的普通行。
  - 支持 UTF-8、GB18030/GBK、EUC-KR 字符集自动识别或手工指定。
- 提供研究辅助命令：
  - `inspect`
  - `inspect-ctl`
  - `scan-system`
  - `scan-partitions`

## 快速开始

```powershell
go test ./...
go build -o .\bin\dmdul.exe .\cmd\dmdul
```

导出建表 DDL：

```powershell
.\bin\dmdul.exe export-ddl `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -out oldpro\dm_offline_schema.sql `
  -owner all
```

导出表数据 INSERT：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\dm_offline_data.sql
```

更多示例见 [使用示例](docs/usage.md)。

## 文档

- [安装方式](docs/install.md)
- [使用示例](docs/usage.md)
- [配置和参数说明](docs/config.md)
- [本地开发、测试、构建说明](docs/development.md)
- [版本变更记录](CHANGELOG.md)
- [逆向扫描笔记](docs/offline-system-scan.md)
- [系统字典字段笔记](docs/system-dictionary-fields.md)

## 项目目录

```text
cmd/dmdul/          CLI 入口
internal/cli/       命令行参数和输出
internal/dm/        达梦 SYSTEM.DBF、dm.ctl、DDL、数据行解析
internal/storage/   文件检查和二进制采样
internal/version/   版本信息
docs/               用户文档和研究笔记
research/           临时研究脚本和实验记录
```

## 重要提醒

- 不要把生产环境的 `*.DBF`、`dm.ctl`、`dm.ini`、导出的 SQL 数据文件提交到公开仓库。
- 当前工具只读取离线文件，不会修改数据库文件。
- 离线恢复结果受达梦版本、页大小、字符集、表类型、行格式影响。导出的 SQL 必须人工审核。
- 行外大 LOB、迁移行、链式行、复杂损坏页等场景仍需要继续验证。

## 开源协议

本项目使用 [MIT License](LICENSE)。
