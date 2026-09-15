# DM8 HUGE 列存储表离线恢复

本文记录 dmdul 对 DM8 HUGE（列存储）表的当前支持范围、离线文件要求和验证证据。
实现依据是达梦官方的 HFS/AUX 模型，并结合一致性快照做物理差分与回灌验证。

本文第 7 节为 **v0.11.0（2026-09-15）** 的增量，v0.10.0 发布包不包含压缩、多路径和新增标量。

官方文档：[管理列存储表](https://eco.dameng.com/document/dm/zh-cn/pm/manage-column-tables.html)

## 1. 为什么 HUGE 表不能按普通 DBF 表处理

普通行表的数据位于 DBF 的段、簇和数据页中。HUGE 表建立在混合表空间上，完整区数据位于
Huge File System（HFS）目录；事务型 HUGE 表的增删改则位于普通 DBF 中的辅助表。因此，
只扫描 `MAIN.DBF` 会漏掉已经进入 HFS section 的主体数据，只读取 HFS 文件又会漏掉尚未
整理的增量。

```text
SYSTEM.DBF dictionary
        |
        +-- HUGE main table
        +-- table$AUX   section metadata
        +-- table$RAUX  rows not yet moved into a full section
        +-- table$DAUX  logical delete ranges
        +-- table$UAUX  updated column values
                 |
                 v
HFS root/SCH#########/TAB####/COL####_##########.dta
                 |
                 v
        merged logical table rows
```

HFS 目录的典型命名为：

```text
HMAIN/
  SCH000001500/
    TAB1063/
      COL0000_0000000000.dta
      COL0001_0000000000.dta
```

每个 `.dta` 文件先有文件头，后续 section 按 4 KiB 对齐。`$AUX` 的 `COLID`、
`SEC_ID`、`FILE_ID`、`OFFSET`、`COUNT`、`N_LEN`、`CPR_FLAG` 和 `ENC_FLAG`
给出每个列 section 的物理位置和编码状态。

## 2. dmdul 当前恢复路径

`bootstrap;` 会执行以下工作：

1. 识别 HUGE 主表及 `$AUX/$RAUX/$DAUX/$UAUX` 内部对象；
2. 从 `SYSOBJECTS.INFO3` 恢复 `SECTION` 和 `FILESIZE`，从辅助对象判断
   `WITH DELTA` / `WITHOUT DELTA`；
3. 对外只保留 HUGE 主表，内部辅助表不计入 `list table` 和普通用户表数量；
4. 把 HUGE 标志、存储参数和四个辅助表 ID 写入 `dmdul_dict/tables.tsv`。

`unload table` 会按以下顺序恢复数据：

1. 从 `$AUX` 获取每列、每个 section 的 HFS 文件号、偏移、行数和长度；
2. 按 HFS section 流式读取列数据，不把整个 `.dta` 文件读入内存；
3. 沿普通 DBF 的 storage root/page plan 读取 `$RAUX/$DAUX/$UAUX`；
4. 合并 HFS 主体行、RAUX 尾部行、DAUX 删除范围和 UAUX 更新值；
5. 把同一逻辑结果写成 SQL、dmfldr 文本或 DMP。

DDL 会生成 `CREATE HUGE TABLE`，并恢复已经验证的表级存储参数：

```sql
CREATE HUGE TABLE SYSDBA.DMDUL_HUGE_SIMPLE (
    ID INT NOT NULL,
    VAL VARCHAR(20),
    TAG CHAR(1)
)
STORAGE(SECTION(1024), FILESIZE(16), WITH DELTA, ON MAIN);
```

## 3. 离线文件准备

恢复 HUGE 表时，普通 DBF 和 HFS 目录必须来自同一个停库时点或存储一致性快照：

```text
/recover/snap/
  SYSTEM.DBF
  MAIN.DBF
  other_tablespace.DBF
  dm.ctl                 # 可选，仅作映射核对
  HMAIN/                 # MAIN 混合表空间的 HFS 根
  other_hfs_root/        # 目标 HUGE 表使用时必须提供
```

`data_dir` 应指向上述共同父目录：

```text
DMDUL> set system /recover/snap/SYSTEM.DBF;
DMDUL> set data_dir /recover/snap;
DMDUL> bootstrap;
DMDUL> describe SYSDBA.DMDUL_HUGE_SIMPLE;
DMDUL> unload table SYSDBA.DMDUL_HUGE_SIMPLE;
```

`describe` 会显示：

```text
storage= HUGE
huge= SECTION(1024), FILESIZE(16 MiB), WITH DELTA, aux_ids= 1064/1065/1066/1067
```

卸载统计会额外显示 `HUGE tables selected`、`HUGE sections read` 和
`HUGE HFS files read`。

## 4. 已验证范围

| 能力 | 当前状态 |
| --- | --- |
| HUGE 主表与四类辅助对象识别 | 已验证 |
| `CREATE HUGE TABLE`、SECTION、FILESIZE、WITH DELTA、ON 表空间 | 已验证 |
| `INT/BIGINT/SMALLINT/DOUBLE/DATE` 可空定长列 | v0.10.0 已验证 |
| 可空/非空 `VARCHAR`、`CHAR` HFS 变长列 | 已验证 |
| `$RAUX` 未满 section 行 | 已验证 |
| `$DAUX` DELETE 与 `$UAUX` UPDATE 合并 | 已验证 |
| SQL 导出和回灌 | 已验证 |
| DMP 导出并由官方 `dimp` 导入 | 已验证 |
| dmfldr 导出与官方 dmfldr 装载 | 已验证 |
| WITHOUT DELTA 数据 | 已实现同一路径，仍缺独立初始化参数样本 |
| ZIP level 2 / 9、Snappy level 10 | v0.11.0：真实 section 与 SQL 往返已验证；其他级别未逐个实测 |
| 多 HFS path | v0.11.0：两条路径、文件轮转、完整文件身份匹配已验证 |
| BIT/TINYINT/REAL/FLOAT、BINARY/VARBINARY、TIME/TIMESTAMP/带时区、INTERVAL | v0.11.0：本页第 7 节样本已验证 |

实机使用 1024 行 section、16 MiB 文件、`WITH DELTA` 的 ARM64 DM8 样本验证：

- 一个完整 HFS section 加 `$RAUX` 共恢复 1499 行；
- 仅有 `$RAUX`、尚未形成完整 HFS section 的小表恢复 100 行，SQL 与 dmfldr 回灌
  双向 `MINUS` 均为 0；
- 包含两个完整 HFS section 加尾部 RAUX 的表恢复 2500 行，SQL 回灌双向 `MINUS` 为 0；
- `$DAUX` 删除和 `$UAUX` 更新后的 SQL 回灌双向 `MINUS` 为 0；
- 表级 DMP 经官方 `dimp REMAP_SCHEMA` 导入 1499 行，与同一快照的 SQL 回灌表
  双向 `MINUS` 均为 0。

## 5. 安全边界

当前实现坚持“不能证明就不解码”：

- 压缩只接受已经验证的 ZIP/Snappy 信封；未知 transform（包括 `0x401` DECIMAL 专用打包）
  明确拒绝。v0.10.0 发布包仍拒绝所有压缩 section；
- `ENC_FLAG != N` 的加密 section 会明确报错；
- 可解码类型及版本边界见第 4、7 节；NUMBER/DECIMAL 不套用行存 NUMBER 解码器；
- 多路径在 `data_dir` 下寻找全部匹配表目录，按文件头身份匹配。缺文件、重复身份、
  截断头会报错；不能把不同快照的 HFS 目录混放在一起；
- 原始 DMASM 成员盘中的 HFS 文件尚未接入 ASM 逻辑 Reader，当前 DMASM 路径只覆盖 DBF；
- `check pages` 当前检查 DBF 页，不检查 `.dta` section 校验和；
- 表/列级 `STAT NONE`、压缩级别、压缩算法、加密和 HUGE 日志属性尚未恢复到 DDL。

为避免异常元数据或超大增量把进程内存耗尽，单次 `$UAUX` 加载最多保留 200 万个唯一
更新且解码值合计不超过 256 MiB；单个 section 的全部变长列 offset 表合计不超过
256 MiB（v0.10.0 将定长 NULL 位图也计入这个合计上限）。超过限制时工具会停止并报告原因，
不会自动扩大内存上限。

遇到上述边界时，不要手工删除错误继续导入。应保存 `SYSTEM.DBF`、普通 DBF、完整 HFS
目录、`dmdul_dict` 和 `dul.log`，用最小样本补充格式证据后再扩展解析器。

## 6. 定长列补充实验（v0.10.0）

2026-09-06，x86-64 DM8 `03134284336-20250117-257733-20132` 的独立实例新增
`HFS_PROBE`：2500 行、7 列、1024 行 section，两个完整 section 加 452 行 RAUX。
SQL 导回普通对照表后双向 MINUS 均为 0；总行数 2500，NI 非 NULL 1667，NB 非 NULL 1875。

| 类型 | HFS 未压缩定长宽度 | 当前解码 |
| --- | ---: | --- |
| INT | 4 | little-endian int32 |
| BIGINT | 8 | little-endian int64 |
| SMALLINT | 4 | int32 存储，再检查 int16 值域；不能套普通行的 2 字节布局 |
| DOUBLE | 8 | little-endian IEEE-754 |
| DATE | 13 | 年 u16、月、日；其余只接受已核实的零时间部分与 1000 标记，不外推 BC/时区 |

可空定长列的 presence bitmap 位于 section 尾部：

```text
bitmap_bytes = ceil(COUNT / 8)
bitmap_start = section_offset + N_LEN - bitmap_bytes
present(row_index) = bitmap[row_index / 8] & (0x80 >> (row_index % 8))
```

位为 1 表示有值，0 表示 NULL。NULL 仍占定长数据位置，不能按零值判断 NULL。
例如每第三行 NULL 的前 8 行位图是 `0xDB`；每第四行 NULL 是 `0xEE`。
读取前检查 payload/位图不重叠，并将零位数量与 `$AUX.N_NULL` 对比；不一致则停止该表。
单个位图最多 32 MiB。`$AUX.CHKSUM` 已取得样本，但算法尚未确认，此次不宣称支持 HFS 校验和。

## 7. 压缩、多路径与标量（v0.11.0）

### 压缩信封

独立 DM8 实例的三个测试表分别使用 level 2、9、10；每表包含可空 INT、VARCHAR、BIGINT。
三个列的首个完整 section 保存为 `internal/dm/testdata/hfs_compressed/` 中九个小样本。

| section 相对偏移 | 已验证含义 |
| --- | --- |
| `+0x04` u32 LE | 物理 section 长度，与 AUX N_LEN 对照 |
| `+0x08` u32 LE | 解压后总长度，包含 128 字节 section 头 |
| `+0x0C` u32 LE | `0` 普通布局、`1` 通用压缩；`0x401` 等专用打包不支持 |
| `+0x18` u16 LE | 列数据类型 ID，不能拿旁边的列号当压缩算法 |
| `+0x80` | `01 + 解压后 body 长度 u32 LE + payload` |

ZIP payload 为 zlib 流；Snappy payload 为压缩长度 u32 LE 加一个 raw Snappy block。
解压后字节与未压缩 section 的 body 一致，再复用 offset/presence bitmap/标量解析。
物理尾部必须为零填充，不忽略多余流或非零尾部。

解压 section 用临时文件承接，结束或报错时关闭并删除。单 section 解压后上限 256 MiB；
Snappy 单 block 的压缩和解压长度分别限制为 64 MiB。临时目录必须有可用空间。
ZIP 的 zlib 校验、长度校验和结构校验 **不是完整的 HFS CHKSUM 支持**。

### 多路径文件号

```text
AUX.FILE_ID: high 8 bits = HFS path index, low 24 bits = file sequence
filename:   COL<column>_<file sequence>.dta
header:     group(u16), schema(u32), table(u32), column(u16), full FILE_ID(u32)
```

不能按目录字典序推断路径号，也不能只凭同名文件选择。读取前同时比对 group、schema ID、
table ID、column ID 和完整 FILE_ID。实测 11,000 行、16 MiB 列文件在两条路径间轮转；
十个完整 section 加 760 行 RAUX，导回后双向 MINUS 为 0。

### 新增标量

| 类型 | 已验证 HFS 布局 |
| --- | --- |
| BIT/BOOL/BOOLEAN、TINYINT | 4 字节 int32 存储，额外检查布尔/小整数值域 |
| REAL / FLOAT | IEEE float32 / float64，以字典精度确定宽度 |
| BINARY / VARBINARY | 变长 offset 表，不能按声明长度直接切 section |
| TIME / TIMESTAMP / TIMESTAMP WITH TIME ZONE | 13 字节结构；时间与普通行 5/8 字节 packed 布局不同 |
| INTERVAL YEAR TO MONTH | 12 字节结构 |
| INTERVAL DAY TO SECOND | 24 字节结构 |

13 字节结构中纳秒是 `raw[7:10]` 加 `raw[12]` 组成的 LE u32；`raw[10:12]` 保存
时区分钟或未带时区的 1000 标记。TIME 的样本基准日期是 1900-01-01。
解码检查日期、时分秒、纳秒、时区及精度范围；不外推 BC 或未验证的地方时区布局。

本轮 16 张表共 48,500 行 SQL 回灌全部双向 MINUS 为 0。每张类型表均包含 NULL，
同时覆盖 HFS 完整区和 RAUX 尾部。`F_DEC` 的 2500 行虽成功造数，但 section 采用
未解码的 `0x401` 打包，**不计入通过数量**。新增类型未逐项重跑 DMP/dmfldr 往返，
不能将这批 SQL 验证当作三格式全部验证。详见 [实测记录](extended-compatibility-20260915.md)。
