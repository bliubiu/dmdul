# 配置和参数说明

`dmdul` 当前没有独立配置文件，所有配置通过命令行参数传入。本页说明常用参数含义和默认值。

## 通用输入文件

| 参数 | 适用命令 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-file` | `scan-system`、`export-ddl`、`export-data`、`scan-partitions` | `SYSTEM.DBF` | 达梦系统表空间文件路径。 |
| `-ctl` | `scan-system`、`export-ddl`、`export-data` 可选；`inspect-ctl`、`scan-partitions` 需要显式提供 | `SYSTEM.DBF` 同目录下存在时自动使用 | 达梦控制文件路径。 |
| `-charset` | `export-ddl`、`export-data`、`scan-partitions` | `auto` | 文本解码字符集，支持 `auto`、`utf-8`、`gb18030`、`gbk`、`euc-kr`。 |

`-ini` 参数已经废弃，保留只是为了兼容旧命令；当前解析不依赖 `dm.ini`。

## export-ddl 参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-out` | `dm_offline_default_all.sql` | 输出 DDL SQL 文件。 |
| `-owner` | `all` | 用户过滤。可填 `all`、单个用户、多个用户逗号分隔。 |

示例：

```powershell
.\bin\dmdul.exe export-ddl -file SYSTEM.DBF -out schema.sql -owner HR_TEST,SYSDBA
```

`export-ddl` 的 `-ctl` 是可选参数；有 `dm.ctl` 时会补充数据库名和表空间名，
没有时仍可只根据 `SYSTEM.DBF` 导出用户、表、字段、索引、约束和注释等 DDL。

## export-data 参数

`export-data` 的 `-ctl` 是可选参数；省略且默认 `dm.ctl` 不存在时，会根据
DBF 页头识别数据文件的 `(表空间号, 文件号)`。`-data-dir` 默认是 `SYSTEM.DBF`
所在目录。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-data-dir` | `SYSTEM.DBF` 所在目录 | 用户表空间 DBF 文件所在目录。 |
| `-out` | `dm_offline_default_data.sql` | 输出 INSERT SQL 文件。 |
| `-owner` | `all` | 用户过滤。 |
| `-table` / `-tables` | `all` | 表过滤，推荐 `OWNER.TABLE_NAME`。多个表用逗号分隔。 |
| `-exclude` | 空 | 排除表，推荐 `OWNER.TABLE_NAME`。 |
| `-max-rows` | `0` | 最大处理行数，`0` 表示不限制。 |
| `-failed-comments` | `false` | 是否把失败行诊断写入 SQL 注释。 |

示例：

```powershell
.\bin\dmdul.exe export-data `
  -file SYSTEM.DBF `
  -out data.sql `
  -table HR_TEST.T_LOG_HEAP
```

## 字符集

`auto` 会优先读取 `SYSTEM.DBF` 第 4 页偏移 `0x2D` 的 `UNICODE_FLAG`：

| 标记 | 字符集 |
| ---: | --- |
| `0` | GB18030 |
| `1` | UTF-8 |
| `2` | EUC-KR |

如果输出对象名、字段名或数据文本乱码，可以手工指定：

```powershell
.\bin\dmdul.exe export-ddl -file SYSTEM.DBF -out schema.sql -charset gb18030
```

## Git 忽略建议

生产文件和导出结果通常包含敏感信息，建议不要提交：

- `*.DBF`
- `*.ctl`
- `dm.ini`
- `*.sql`
- `oldpro/`
- `windowdameng/`
- `bin/`

项目根目录的 `.gitignore` 已包含这些常见规则。
