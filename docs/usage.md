# 使用示例

以下示例假设程序位于 `.\bin\dmdul.exe`。当前推荐使用交互式入口，旧的
`export-ddl`、`export-data` 一次性命令仍保留用于调试和兼容，但不再作为主要操作方式。

## 准备目录

建议把可执行文件、参数文件和离线数据库文件放在同一个工作目录，或者在交互界面中用
`set` 命令指定路径：

```text
dmdul.exe
init.dul
control.dul
dmdul_dict/
dul.log
SYSTEM.DBF
MAIN.DBF
TBS_*.DBF
```

`dm.ctl` 是可选增强文件。有它时可以补充数据库名、表空间名和数据文件路径；没有它时，
数据抽取会根据 DBF 页头识别 `(表空间号, 文件号)`。

## 启动 DMDUL

```powershell
.\bin\dmdul.exe
```

进入交互界面后会看到提示符：

```text
DMDUL>
```

查看帮助：

```text
DMDUL> help;
```

## 设置参数

如果当前目录下就有 `SYSTEM.DBF`，可以直接执行 `bootstrap;`。否则先设置路径：

```text
DMDUL> set system D:\temp\oldpro\SYSTEM.DBF;
DMDUL> set data_dir D:\temp\oldpro;
DMDUL> set control D:\temp\oldpro\dm.ctl;
DMDUL> set output_dir D:\temp\oldpro\out;
DMDUL> set data_format sql;
DMDUL> set charset auto;
```

查看当前参数：

```text
DMDUL> show parameter;
```

## 加载字典

```text
DMDUL> bootstrap;
```

`bootstrap` 会读取 `SYSTEM.DBF`，识别页大小、簇大小、页数、字符集，并加载用户、表、字段等字典信息。
同时会扫描 `data_dir` 下的 DBF 页头，生成 `control.dul` 数据文件清单，并把字典摘要写入
`dmdul_dict` 文本目录。成功后还会把识别到的数据库字符集回写到 `init.dul`
的 `charset` 参数。成功后才能继续执行 `list` 和 `unload`。

`dmdul_dict` 目录当前包含：

```text
meta.tsv
users.tsv
tables.tsv
columns.tsv
```

这些文件可以人工审查和修正。再次进入 DMDUL 后，可以不重扫 `SYSTEM.DBF`，直接加载文本字典：

```text
DMDUL> load dictionary;
```

如果内存中还没有字典，执行 `list user`、`list table`、`unload table`、`unload user` 前也会尝试自动从
`dmdul_dict` 加载。

## 查看用户和表

列出用户/owner：

```text
DMDUL> list user;
```

`list user` 会先显示当前字典来源、字典目录和字典行数统计，再列出用户/owner。

列出某个用户下的表：

```text
DMDUL> list table HR_TEST;
```

输出中会显示表名、table id、字段数、表空间、存储组织和是否分区。

## 恢复单表

```text
DMDUL> unload table HR_TEST.EMP_INFO;
```

默认会生成两个文件。未设置 `output_dir` 时，如果设置过 `data_dir`，文件会输出到
`data_dir`；如果也没有设置 `data_dir`，文件会输出到当前目录。`control.dul` 和
`dul.log` 也遵循同样的目录规则。交互模式还会自动生成 `init.dul`：
未设置 `data_dir` 时写到当前目录，设置 `data_dir` 后写到 `data_dir`。

```text
HR_TEST_EMP_INFO_ddl.sql
HR_TEST_EMP_INFO_data.sql
```

也可以指定输出前缀：

```text
DMDUL> unload table HR_TEST.EMP_INFO to emp_info;
```

生成：

```text
emp_info_ddl.sql
emp_info_data.sql
```

导出 CSV 数据：

```text
DMDUL> set data_format csv;
DMDUL> unload table HR_TEST.EMP_INFO;
```

生成：

```text
HR_TEST_EMP_INFO_ddl.sql
HR_TEST_EMP_INFO_data.csv
```

## 恢复一个用户

```text
DMDUL> unload user HR_TEST;
```

默认生成：

```text
HR_TEST_ddl.sql
HR_TEST_data.sql
```

如果 `data_format=csv`，`unload user` 会生成一个用户级 DDL 文件，并按表分别生成
CSV 文件；没有数据的表不会生成空 CSV。例如：

```text
HR_TEST_ddl.sql
HR_TEST_EMP_INFO_data.csv
HR_TEST_T_LOG_HEAP_data.csv
```

也可以指定输出前缀：

```text
DMDUL> unload user HR_TEST to hr_test_all;
```

## init.dul 示例

如果工作目录下存在 `init.dul`，DMDUL 启动时会自动读取；每次执行 `set`
命令后会同步写入：

```text
system=D:\temp\oldpro\SYSTEM.DBF
control=D:\temp\oldpro\dm.ctl
data_dir=D:\temp\oldpro
output_dir=
data_format=sql
charset=auto
log=
```

如果运行过程中手工修改了 `init.dul`，可以重新加载：

```text
DMDUL> load init;
DMDUL> show parameter;
```

## 退出

```text
DMDUL> exit;
```

## 研究辅助命令

以下一次性命令仍可用于排查文件和验证底层解析：

```powershell
.\bin\dmdul.exe inspect -file oldpro\SYSTEM.DBF
.\bin\dmdul.exe inspect-ctl -ctl oldpro\dm.ctl
.\bin\dmdul.exe scan-system -file oldpro\SYSTEM.DBF
.\bin\dmdul.exe scan-partitions -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl -owner all
```

## 输出结果建议

导出的 SQL 建议按顺序使用：

1. 先审查并执行 DDL 文件。
2. 再审查并执行 INSERT 文件。
3. 对关键表做行数和抽样内容校验。
