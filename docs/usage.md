# 使用示例

以下示例假设程序位于 `.\bin\dmdul.exe`。数据库扫描、字典加载、DDL 导出、
数据恢复全部通过交互式入口完成，不再提供功能性命令行子命令。

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
views.tsv
sequences.tsv
routines.tsv
triggers.tsv
synonyms.tsv
tab_privs.tsv
```

这些文件可以人工审查和修正。再次进入 DMDUL 后，可以不重扫 `SYSTEM.DBF`，直接加载文本字典：

```text
DMDUL> load dictionary;
```

加载后的 `dmdul_dict` 会真正参与后续恢复：DDL 和数据导出时，用户、表名、字段名、字段类型、
默认值、表空间、存储组织、视图、序列、存储过程/函数/包、触发器、同义词和对象授权会优先使用文本字典中的内容；
`SYSTEM.DBF` 仍用于辅助定位索引、约束、分区和数据页等物理信息。Windows 下手工保存为带
UTF-8 BOM 的 TSV 文件也可以正常读取。

`tables.tsv` 还预留了 `header_file`、`header_block`、`bytes`、`blocks`、`extents`
字段，对应在线 `DBA_SEGMENTS` 的段信息。补齐这些字段后，数据抽取会按段页范围过滤候选页，
能减少同名表或相似行格式造成的误匹配。

如果内存中还没有字典，执行 `list user`、`list table`、`unload table`、`unload user`、
`unload database` 前也会尝试自动从 `dmdul_dict` 加载。

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

## DROP / TRUNCATE 残留页恢复

`recover table` 用于在表被 `TRUNCATE` 或 `DROP` 后，数据块尚未被新写入覆盖时，扫描数据文件中的残留行。它会使用字典中的字段定义解码行，并按页头里的 storage/assist id 过滤候选页。

TRUNCATE 场景中，表结构通常仍在当前 `SYSTEM.DBF` 中：

```text
DMDUL> bootstrap;
DMDUL> recover table USERS1.T_TEST;
```

DROP 场景中，当前字典里可能已经没有表定义，需要先加载 DROP 前保存的 `dmdul_dict`，或人工在 `tables.tsv`、`columns.tsv` 中补齐表结构和 `storage_id/assist_ids`：

```text
DMDUL> load dictionary;
DMDUL> recover table USERS1.T_TEST to users1_t_test_drop_recover;
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

## 恢复整库

```text
DMDUL> unload database;
```

默认生成：

```text
DATABASE_ddl.sql
DATABASE_data.sql
```

`DATABASE_ddl.sql` 包含可识别用户、用户表、视图、序列、存储过程/函数/包、触发器、同义词和表/视图/序列授权 DDL；`DATABASE_data.sql`
包含所有可识别用户表的 INSERT 数据。

如果 `data_format=csv`，`unload database` 会生成一个全库 DDL 文件，并按 owner/table
分别生成 CSV 文件；没有数据的表不会生成空 CSV。例如：

```text
DATABASE_ddl.sql
DATABASE_HR_TEST_EMP_INFO_data.csv
DATABASE_SYSDBA_T_data.csv
```

也可以指定输出前缀：

```text
DMDUL> unload database to dmdb_all;
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

## 命令行边界

直接运行 `.\bin\dmdul.exe` 进入交互界面。当前只保留两个非恢复子命令：

```powershell
.\bin\dmdul.exe help
.\bin\dmdul.exe version
```

`inspect`、`inspect-ctl`、`scan-system`、`scan-partitions`、`export-ddl`、
`export-data` 均已移除。对应的解析和恢复能力由 `bootstrap`、`list`、`unload`
和 `recover` 等交互命令统一提供。

## 输出结果建议

导出的 SQL 建议按顺序使用：

1. 先审查并执行 DDL 文件。
2. 再审查并执行 INSERT 文件。
3. 对关键表做行数和抽样内容校验。
