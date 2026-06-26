# 使用示例

以下示例假设在项目根目录执行，程序位于 `.\bin\dmdul.exe`。

## 查看帮助

```powershell
.\bin\dmdul.exe help
```

## 检查文件头

```powershell
.\bin\dmdul.exe inspect -file oldpro\SYSTEM.DBF
.\bin\dmdul.exe inspect -file oldpro\MAIN.DBF -sample 256
```

## 检查 dm.ctl

```powershell
.\bin\dmdul.exe inspect-ctl -ctl oldpro\dm.ctl
```

## 扫描 SYSTEM.DBF 核心字典对象

```powershell
.\bin\dmdul.exe scan-system -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl
```

## 导出建表 DDL

默认从当前目录读取 `SYSTEM.DBF` 和 `dm.ctl`，输出到 `dm_offline_default_all.sql`：

```powershell
.\bin\dmdul.exe export-ddl
```

指定输入和输出：

```powershell
.\bin\dmdul.exe export-ddl `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -out oldpro\dm_offline_schema.sql
```

导出所有用户对象：

```powershell
.\bin\dmdul.exe export-ddl `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -out oldpro\dm_offline_all.sql `
  -owner all
```

`export-ddl` 会在表 DDL 前输出可恢复的普通用户和角色授权，例如：

```sql
CREATE USER HR_TEST IDENTIFIED BY "dmdul_default_password" DEFAULT TABLESPACE "MAIN" TEMPORARY TABLESPACE "TEMP";
GRANT PUBLIC TO HR_TEST;
GRANT RESOURCE TO HR_TEST;
```

密码哈希默认不会从 `SYSUSERS` 导出，生成脚本使用占位密码，恢复后需要人工修改。
当前版本已恢复角色授权；对象权限授权仍需要继续校验 `SYSGRANTS.PRIVID`
到权限名称的映射。

只导出指定用户：

```powershell
.\bin\dmdul.exe export-ddl `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -out oldpro\hr_test_schema.sql `
  -owner HR_TEST
```

导出多个用户：

```powershell
.\bin\dmdul.exe export-ddl `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -out oldpro\some_users_schema.sql `
  -owner HR_TEST,SYSDBA
```

手工指定字符集：

```powershell
.\bin\dmdul.exe export-ddl `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -out oldpro\schema_gb18030.sql `
  -charset gb18030
```

## 导出表数据 INSERT

导出所有可识别用户表数据：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\dm_offline_data.sql
```

只导出一张表，推荐使用 `OWNER.TABLE_NAME`：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\t_log_heap_data.sql `
  -table HR_TEST.T_LOG_HEAP
```

导出多张表：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\bin_test_data.sql `
  -table SYSDBA.BIN_TEST2,SYSDBA.BIN_TEST2_CHILD
```

导出包含行内 LOB 字段的表：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\t_lob_test_data.sql `
  -table SYSDBA.T_LOB_TEST
```

当前版本支持行内小 `CLOB`/`TEXT` 和行内小 `BLOB`/`IMAGE`；行外大 LOB
仍需要继续验证 LOB 定位符和 LOB 段页格式。

导出全部表，但排除指定表：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\dm_offline_data.sql `
  -table all `
  -exclude SYSDBA.T1
```

限制最多处理行数：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\sample_data.sql `
  -max-rows 100
```

输出失败行诊断注释：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\debug_data.sql `
  -table HR_TEST.T_LOG_HEAP `
  -failed-comments
```

默认不会在 INSERT 前写入页号、slot、原始数据等调试注释。

## 扫描分区元数据

```powershell
.\bin\dmdul.exe scan-partitions `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -owner all
```

`export-ddl` 已经会合并分区表 DDL，`scan-partitions` 主要用于研究和诊断。

## 常见文件关系

- `SYSTEM.DBF`：系统字典，负责恢复表、字段、索引、约束、注释、存储信息。
- `dm.ctl`：控制文件，负责恢复数据库名、表空间名、数据文件路径信息。
- `MAIN.DBF` / `TBS_*.DBF`：用户表空间数据文件，负责恢复表数据。
- `-data-dir`：这些 DBF 文件所在目录。

## 输出结果建议

导出的 SQL 建议按顺序使用：

1. 先审查并执行 DDL 文件。
2. 再审查并执行 INSERT 文件。
3. 对关键表做行数和抽样内容校验。
