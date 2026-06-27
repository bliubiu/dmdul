# 配置和参数说明

DMDUL 交互式模式优先读取当前目录下的 `init.dul`。也可以进入 `DMDUL>` 后使用
`set` 命令临时修改参数。

## init.dul

示例：

```text
system=D:\temp\oldpro\SYSTEM.DBF
control=D:\temp\oldpro\dm.ctl
data_dir=D:\temp\oldpro
output_dir=D:\temp\oldpro\out
data_format=sql
charset=auto
log=dul.log
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `system` | `SYSTEM.DBF` | 达梦系统表空间文件路径。 |
| `control` | 自动查找 `SYSTEM.DBF` 同目录下的 `dm.ctl` | 可选控制文件路径。 |
| `data_dir` | `SYSTEM.DBF` 所在目录 | 用户表空间 DBF 文件所在目录。 |
| `output_dir` | 未设置时：如果设置过 `data_dir` 则使用 `data_dir`，否则使用当前目录 | DDL、数据文件、`control.dul` 和 `dul.log` 的输出目录。 |
| `data_format` | `sql` | 数据导出格式，支持 `sql` 和 `csv`。 |
| `charset` | `auto` | 字典和数据文本解码字符集。 |
| `log` | `dul.log`，目录规则同 `output_dir` | 交互式执行日志。 |

## 交互式 set 命令

```text
DMDUL> set system D:\temp\oldpro\SYSTEM.DBF;
DMDUL> set control D:\temp\oldpro\dm.ctl;
DMDUL> set data_dir D:\temp\oldpro;
DMDUL> set output_dir D:\temp\oldpro\out;
DMDUL> set data_format csv;
DMDUL> set charset gb18030;
DMDUL> show parameter;
```

修改 `system`、`control` 或 `charset` 后，需要重新执行：

```text
DMDUL> bootstrap;
```

## 字符集

`auto` 会优先读取 `SYSTEM.DBF` 第 4 页偏移 `0x2D` 的 `UNICODE_FLAG`：

| 标记 | 字符集 |
| ---: | --- |
| `0` | GB18030 |
| `1` | UTF-8 |
| `2` | EUC-KR |

如果输出对象名、字段名或数据文本乱码，可以手工指定：

```text
DMDUL> set charset gb18030;
DMDUL> bootstrap;
```

## control.dul

`control.dul` 是 DMDUL 的数据文件清单。执行 `bootstrap;` 时会根据 `data_dir`
下的 DBF 页头自动生成，也可以人工维护。当前格式如下：

```text
datafile 0 0 SYSTEM D:\temp\oldpro\SYSTEM.DBF
datafile 1 0 ROLL D:\temp\oldpro\ROLL.DBF
datafile 3 0 TEMP D:\temp\oldpro\TEMP.DBF
datafile 4 0 MAIN D:\temp\oldpro\MAIN.DBF
datafile 5 0 TBS_BIN_TEST D:\temp\oldpro\TBS_BIN_TEST01.DBF
```

字段含义依次是：`datafile`、表空间号、文件号、表空间名、数据文件路径。
当前版本的数据文件识别优先使用 `dm.ctl`；没有 `dm.ctl` 时，会读取 `control.dul`，
并继续扫描 `data_dir` 下的 DBF 页头识别 `(表空间号, 文件号)`。

没有 `dm.ctl` 时，DMDUL 会内置基础表空间名映射：`0=SYSTEM`、`1=ROLL`、`3=TEMP`、
`4=MAIN`。业务自定义表空间会优先使用 `control.dul` 中的名称；如果没有清单，
会根据文件名做保守推断，例如 `TBS_BIN_TEST01.DBF` 推断为 `TBS_BIN_TEST`。

## Git 忽略建议

生产文件和导出结果通常包含敏感信息，建议不要提交：

- `*.DBF`
- `*.ctl`
- `dm.ini`
- `init.dul`
- `control.dul`
- `dul.log`
- `*.sql`
- `oldpro/`
- `windowdameng/`
- `bin/`

项目根目录的 `.gitignore` 已包含这些常见规则。
