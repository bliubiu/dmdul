# 扩展兼容验证：HUGE 与对象恢复

日期：2026-09-15。本文记录 **v0.11.0** 的实现与实测；v0.10.0 二进制不包含本轮新增能力。
普通 DBF 的 SM3、系统/列级权限在 v0.10.0 已实现，本轮不重复将它们列为新增。

## 1. 请求完成情况

| 项目 | 结论与边界 |
| --- | --- |
| DM9 4 KiB、CASE_SENSITIVE=0、PAGE_CHECK 0/1/2/3 | 现有单 build 矩阵已复测，见 [Hardening](hardening.md)；4 KiB + HASH 被 dminit 拒绝，HASH 用 8/16/32 KiB 验证 |
| PAGE_CHECK=2 SM3 | v0.10.0 已支持，本轮质量测试继续覆盖 SM3/SHA256 sector 校验和保护字节还原 |
| DM9 第二个 build | 未完成；已安装软件与现有 ISO 的完整 build 相同，未找到另一个 dmserver 二进制 |
| HUGE 压缩 | 新增已验证 ZIP level 2/9、Snappy level 10；不是所有 level/专用压缩形式全覆盖 |
| HUGE 可空定长与更多类型 | 新增 BIT/TINYINT/REAL/FLOAT、二进制、时间/时区、两类 INTERVAL；NUMBER/DECIMAL 的专用打包仍拒绝 |
| 多 HFS path | 新增完整文件身份匹配，两条路径和文件轮转实测通过 |
| HFS 校验和、DMASM HFS | 未完成；zlib 自带校验不能替代 HFS 的完整校验机制 |
| DMASM 单文件超过 65535 AU、REDO 重放、已删除文件救援 | 未完成；没有新增裸盘物理证据，不把合成地址范围测试当成真实验证 |
| 真实系统权限、列级权限 | v0.10.0 已实现；本轮没有重新开放未知权限编号 |
| 自定义类型与类型体 | 新增扫描、types.tsv、SQL/DMP；不表示支持自定义类型列的数据解码 |
| 目录对象 | 新增 INFO6 路径、directories.tsv、FULL SQL/DMP；不创建操作系统目录，不补造目录授权 |
| 物化视图 | 新增已验证简单定义，延迟构建；不恢复其物化数据、原构建状态、日志和刷新任务 |
| 作业、策略 | 未完成；需要独立系统表、启用状态和执行安全性实验 |

## 2. 实验隔离与文件

DM8：`03134284336-20250117-257733-20132`，x86-64。使用独立 UNDOLAB 实例（端口 5439），
每轮建表或导回后正常关闭；离线导出在关闭后进行。没有停止主实例，也没有改写原始数据文件。

为排除跨模式重映射影响，另建空白 HXIMP 实例（端口 5441），用同一模式名分别导入官方
`dexp` 对照文件和 dmdul 生成文件。试验结束后 HXIMP 正常关闭。

服务器研究目录为 `/dmdata/dmdul-extended-20260915/`，证据包括：

| 相对路径 | 内容 |
| --- | --- |
| `create2/` | HUGE 建表、造数、AUX 列表和在线样本 |
| `multi2/` | 双 HFS path 的 11,000 行造数 |
| `offline1/output/` | 16 张表的 SQL 数据导出 |
| `roundtrip/results.tsv` | 逐表行数与双向 MINUS 结果 |
| `objects7/`、`objects8/` | 类型/物化视图实验对象及官方 dexp 对照 |
| `offline_objects1/output/` | 从持久化字典重载后的 SQL 与 OWNER DMP |
| `same_schema2/` | 原模式名导回、成员函数执行、对象状态及原生对照日志 |
| `directory_verify/` | DIRECTORY 原生记录导入与路径查询 |
| `quality.log` | Linux race、九组 fuzz 和构建日志 |

这些完整目录不纳入公开仓库。公开回归只保留确定性造数的九个短 HFS section，不包含用户密码、
原库表数据或完整 dexp 文件。完整 dexp 可能含账号信息，不应直接上传。

## 3. HUGE 逐行比对

| 表组 | 数量 | 每表行数 | 比对 |
| --- | ---: | ---: | --- |
| PLAIN / ZIP2 / ZIP9 / SNAP10 | 4 | 2500 | SQL 导回普通对照表，双向 MINUS=0 |
| F_BOOL / F_TINY / F_REAL / F_FLOAT | 4 | 2500 | 同上 |
| F_VBIN / F_BIN / F_TIME / F_TS / F_TZ / F_IYM / F_IDS | 7 | 2500 | 同上 |
| PATHS | 1 | 11000 | 两条 HFS path，双向 MINUS=0 |
| 合计 | 16 | 48500 | 0 缺失，0 多出 |

每张类型表都有可空列，2500 行覆盖两个 1024 行 HFS section 和 452 行 RAUX。
PATHS 覆盖十个完整 section 和 760 行 RAUX；列文件大小为 16 MiB，实际发生跨路径轮转。
压缩测试的 NI 每三行 NULL，TX 每四行 NULL，NB 每五行 NULL，避免只验证非空样本。

`F_DEC` 另有 2500 行，但其 section 使用 `0x401` 专用变换，当前明确拒绝；**未计入 48,500 行**。
本轮新增标量只完成 SQL 往返，未逐项重做 DMP/dmfldr 往返。旧 HUGE 的三格式验证结果不能
替代新类型的往返测试。

物理信封、NULL 位图、FILE_ID 分解和资源限制见 [HUGE 格式](huge-tables.md)。
官方列存文档将 AUX 的 CHKSUM 说明为存储标记位，不能仅凭字段名称推断算法。
[管理列存储表](https://eco.dameng.com/document/dm/zh-cn/pm/manage-column-tables.html)

## 4. 对象字典与输出

### 类型

从 SYSOBJECTS 的 SCHOBJ/CLASS、SCHOBJ/TYPE 与 SYSTEXTS 获取已明确以 `CREATE TYPE` 开始的源码。
SEQNO=0 为 TYPE，SEQNO=1 为 TYPE BODY；不把 Java/native CLASS 任意改写为 TYPE。

`types.tsv` 保存 `type_id / owner / type_name / object_type / sql`。加载时检查类别、ID 和 SQL
对象身份。旧字典没有此文件时可以回退扫描，新字典的空文件表示明确不导出类型。
同一模式内保留类型创建 ID 顺序，先定义后类型体；这不是任意循环依赖的拓扑排序。

SQL 使用独立 `/` 结束符。DMP 使用实测原生记录 25（定义）和 29（类型体），不携带 `/`。
实验中 `Z_POINT_T` 含成员函数，`A_POINTS_T` 是依赖前者的 VARRAY；原模式 DMP 导入无警告，
成员方法调用返回预期值。字典中 CLASS 与 SQL 的 TYPE 是不同层面的名称，DBA_OBJECTS
可能显示 CLASS，不应据此判定类型体丢失。

### 目录

目录路径来自 SYSOBJECTS 的 DIR 对象 INFO6；DBA_DIRECTORIES 的 OWNER 固定为 SYS，
因此它是全局对象，不应按某个表所属模式猜归属。

`directories.tsv` 保存 ID、名称与服务器路径。整库 SQL 生成 `CREATE OR REPLACE DIRECTORY`，
FULL DMP 使用实测记录 36。单独原生目录记录导入无警告，查询路径与导出值一致。
恢复前须人工复核目标服务器路径；工具不创建目录，也不自动授予 READ/WRITE 权限。

### 物化视图

物化视图在 SYSOBJECTS 中也使用 VIEW。INFO1 的 `0x200` 标志与官方 DBA_MVIEWS 定义一致，
SYSTEXTS 的首段可能只有 CREATE 名称，查询在下一段，不能直接输出首段。

本轮差分确认简单形式的刷新方法位、ON COMMIT 位、QUERY REWRITE 位和 ROWID 位。
支持的生成分支是 NEVER REFRESH / REFRESH COMPLETE、FORCE，ON DEMAND/COMMIT，
PRIMARY KEY/ROWID，ENABLE/DISABLE QUERY REWRITE。FAST 只有位定义证据，缺少物化日志恢复，
当前明确拒绝生成。未知组合或复杂形式也拒绝重建。

实测 SQL 和同模式 DMP 均恢复 COMPLETE、FORCE、NEVER、ROWID 四种简单定义，
另验证 `BUILD DEFERRED + REFRESH COMPLETE ON COMMIT` 可以执行。
生成定义统一使用 **BUILD DEFERRED**，避免先于基础表数据装载刷新。
内部 MTAB$_ 表只有在存在对应物化视图且内部标志匹配时才从普通表列表排除，不仅凭名称过滤。

限制：不恢复物化行快照、原 BUILD 状态、物化日志、调度任务。NEVER REFRESH 对象仍需要用户
另行决定其数据恢复办法，不能认为“定义创建成功”就等于数据完整。

### 跨模式导入

本轮 REMAP_SCHEMA 实验中，目标物化视图名被映射，但查询中明确写出的源模式名没有重写，
导致目标用户缺少源表 SELECT 权限。保持原模式名导入新空白实例后成功。
不要为绕过此问题自动扩大授权；应先审核 SQL 引用并按恢复目标修正。
官方参数说明：[dimp 逻辑导入](https://eco.dameng.com/document/dm/zh-cn/pm/dimp-logical%20import.html)。

## 5. 门禁与下一步

- Windows Go 1.26.8 / 1.27.1 完整单元测试通过；Go 1.27.1 vet、调用路径及模块级
  govulncheck 无已知漏洞；新增 Snappy v1.0.0 的许可证随源码与发布包保留。
- Linux 使用 Go 1.27.1，完整 race 与九组各 30 秒 fuzz；加入压缩 body 的错误长度、
  截断、非零填充和资源约束种子。CI 不连接实验机，提交后的远端工作流尚待运行。
- 需要一个**完整 build 编号不同**的 DM9 软件包才能完成第二 build 矩阵。
- DMASM 的三项扩展仍需要一致冷裸盘、官方文件清单与逻辑复制对照；REDO/删除救援必须
  有前后差分快照。没有这些证据，不应向正常读取路径加入推测式重放或复活文件逻辑。
- 作业与策略需要单独验证系统字典、所有者、定义、启用状态和依赖。恢复时不应默认启用
  作业或凭空重建访问策略；本轮没有添加会自动执行调度任务的代码。
