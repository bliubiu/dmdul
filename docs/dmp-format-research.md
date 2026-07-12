# DM8 DMP 逻辑导出格式实验记录

> 本文记录的是基于 DM8 `dexp/dimp` 黑盒差分实验得到的阶段性结论，不是达梦官方文件格式规范。
> 不同 DM8 构建版本、逻辑文件版本、字符集、压缩和加密参数可能存在差异。生成的 DMP 必须先在测试库使用官方 `dimp` 校验和回灌。

## 1. 研究目标

`dmdul` 当前能够把离线恢复的数据写成逐行 `INSERT INTO` SQL，但大数据量下 SQL 解析、网络传输、事务提交和索引维护成本较高。

本实验研究能否把离线解析出的行直接写成 DM8 DMP 文件，再通过官方 `dimp` 的快速装载路径导入：

```text
dimp ... DATA_ONLY=Y TABLE_EXISTS_ACTION=APPEND FAST_LOAD=Y CTRL_INFO=2
```

官方资料：

- [`dexp` 逻辑导出](https://eco.dameng.com/document/dm/zh-cn/pm/dexp-logical-export.html)
- [`dimp` 逻辑导入](https://eco.dameng.com/document/dm/zh-cn/pm/dimp-logical%20import.html)
- [逻辑导入导出功能简介](https://eco.dameng.com/document/dm/zh-cn/pm/dexp-dimp-function-introduction.html)

官方 `dimp` 支持 `SHOW=Y` 查看 DMP 内容，`CTRL_INFO=4` 只校验 MD5 而不导入，`FAST_LOAD=Y` 使用 `dmfldr` 快速装载。

## 2. 实验环境

```text
DM Database Server 64 V8
build: 03134284336-20250117-257733-20132
dexp file_version: 26
page size: 8192
extent size: 16 pages

实例 1: UTF-8   / UNICODE_FLAG=1
实例 2: GB18030 / UNICODE_FLAG=0
实例 3: EUC-KR  / UNICODE_FLAG=2
```

实验对象包含：

- 基础类型表：BIT、BYTE、TINYINT、SMALLINT、INT、BIGINT、DECIMAL、NUMBER、
  REAL、FLOAT、DOUBLE、CHAR、VARCHAR、VARCHAR2、BINARY、VARBINARY、DATE、TIME、
  带时区 TIME、TIMESTAMP、带时区 TIMESTAMP、本地时区 TIMESTAMP、两类 INTERVAL；
- ROWID 表；
- RANGE、LIST、HASH 分区表；
- LOB 表：约 64 MiB 字节的 CLOB 加 64 MiB BLOB，同时包含空 LOB 和 NULL LOB；
- 空表；
- 主键、二级索引、表注释、字段注释；
- 用户、角色授权、视图、序列；
- 20000 行批量表。

对同一张表生成了以下差分样例：

- 两次完全相同参数的未压缩导出；
- `ROWS=N`；
- `DESCRIBE`；
- `FILE_VERSION=9/15/20/26`；
- `COMPRESS=Y COMPRESS_LEVEL=1/9`；
- 精简约束、索引、授权和触发器；
- TABLES、SCHEMAS、OWNER 三种导出模式。

另外用 Go 写入器生成了 GB18030、EUC-KR、分区表和 32 MiB LOB 纯数据 DMP，
并分别使用普通 `dimp` 和 `FAST_LOAD=Y` 回灌。

## 3. 总体布局

未加密 DMP 的总体结构为：

```text
+----------------------+ 0x0000
| fixed header         | 4096 bytes
+----------------------+ 0x1000
| object records       |
| table phase-1        |
| table data phases    |
+----------------------+ payload_end
| footer magic         |
| schema/table index   |
+----------------------+ EOF
```

同一数据库、同一对象和相同参数连续导出的两个 DMP SHA-256 完全相同，说明该实验条件下 DMP 是确定性输出。

## 4. 4KB 文件头

以下字段已经通过差分或官方 `dimp` 回灌确认：

| 偏移 | 长度 | 含义 |
| --- | ---: | --- |
| `0x000` | 4 | 内部文件版本，小端；等于 `FILE_VERSION + 8` |
| `0x004` | 4 | 固定值 1，具体名称待确认 |
| `0x008` | 4 | 导出模式：1=FULL、2=SCHEMAS、3=TABLES、4=OWNER |
| `0x10D` | 8 | footer 起始绝对偏移 `payload_end` |
| `0x115` | 1 | dump encoding code：UTF-8=1、GB18030=10、EUC-KR=6 |
| `0x116` | 2 | `DESCRIBE` 字节长度 |
| `0x118` | 可变 | `DESCRIBE` 内容 |
| `0x318` | 1 | 压缩标志，0=未压缩，1=压缩 |
| `0x320` | 4 | 行格式/快速装载相关标志；当前样例为 1，准确名称待确认 |
| `0x435` | 1 | CASE_SENSITIVE：0=N、1=Y |
| `0x436` | 4 | extent size，小端 |
| `0x43A` | 4 | page size，小端，单位字节 |
| `0x43E` | 2 | UNICODE_FLAG：GB18030=0、UTF-8=1、EUC-KR=2 |
| `0x440` | 4 | PAGE_CHECK |
| `0x745` | 固定区 | 原生文件中的加密算法名称 |
| `0x846` | 固定区 | dexp 构建版本字符串 |
| `0xA4A` | 4 | DDL/对象记录数；纯数据 DMP 为 0 |
| `0xA56` | 16 | 从 `0x1000` 到 EOF 的 MD5 |
| `0xA66` | 1 | 压缩级别 |
| `0xFFF` | 1 | 头部异或校验字节 |

头部校验规则：

```text
XOR(header[0x000:0x1000]) == 0
MD5(file[0x1000:EOF]) == header[0xA56:0xA66]
```

实验已经证明，纯数据 DMP 不需要复制 `dexp` 的完整头模板。版本、模式、
`payload_end`、字符集、`0x320=1`、初始化参数、MD5 和异或校验写对后，当前版本
官方 `dimp` 可以直接识别和导入。使用 `CTRL_INFO=2` 时还应写入
CASE_SENSITIVE、extent size、page size 和 PAGE_CHECK；否则可能出现交互式参数不一致提示。

字符集需要同时写两个字段，不能只写 `UNICODE_FLAG`：

| 数据库字符集 | `0x115` encoding code | `0x43E` UNICODE_FLAG |
| --- | ---: | ---: |
| UTF-8 | 1 | 1 |
| GB18030 | 10 | 0 |
| EUC-KR | 6 | 2 |

`0x320` 不能直接命名为 `UNICODE_FLAG`：把它改成 0 或 2 时，普通导入仍可完成，但
`FAST_LOAD=Y` 会出现未完整装载警告；`dimp` 显示的 dump file code 也不会随该值改成
GB18030 或 EUC-KR。因此代码中暂称 `row_format_flag`，写入器只允许已经回灌验证的值 1。

## 5. 表记录和 phase

表至少包含 phase-1；有数据时包含一个或多个数据 phase。phase 编号从 2 递增。

### 5.1 phase-1

```text
u16 record_type       = 2
u16 marker            = 0xFFFF
u32 table_object_id
u32 phase             = 1
u32 phase_length      -- 从本记录开始到 phase-2 或 footer
u32 reserved          = 0
u8  no_rows_flag      -- 原生空表/ROWS=N 为 1
u32 table_name_length
byte table_name[]
... DDL/object records ...
u16 row_marker_type   = 14
u16 marker            = 0xFFFF
u16 column_count
```

最小纯数据 DMP 可以省略 CREATE TABLE、索引、注释等对象记录，只保留表名和末尾的类型 14/字段数标记。

### 5.2 数据 phase

```text
u16 record_type       = 2
u16 marker            = 0xFFFF
u32 table_object_id
u32 phase             -- 2、3、4 ...
u32 phase_length
u32 row_count         -- 本 phase 完成的行数，或 0xFFFFFFFF 续传标记
u8  reserved          = 0
u32 table_name_length
byte table_name[]
byte rows[]
```

20000 行、约 1.88 MB 的测试表仍只有一个数据 phase。分区表的数据也按父表形成统一
行流，不会按叶子分区各建一个数据 phase；`dimp` 根据分区键把行路由到目标分区。

超大 LOB 会跨多个 phase。原生未压缩 125 MiB LOB 样例产生 13 个数据 phase，
单个 phase 接近 10 MiB。续传规则已经确认：

- 每个 phase 都重新写完整 phase 头和表名；
- `0xFFFE + u64 total_length` 只在长字段开始处写一次；
- 后续 phase 的表名之后直接继续长字段字节，不重复长度；
- phase 内没有完整行、但行仍在继续时，`row_count=0xFFFFFFFF`；
- 某个 phase 完成一行后，写入该 phase 实际完成的行数；
- footer 必须列出 phase-1 和所有数据 phase 的绝对偏移。

把 32 MiB LOB 全写进单个 phase，当前 `dimp` 会报 `Invalid function sequence`。
`dmdul` 原型因此使用 8 MiB 上限主动切分；6-phase 测试文件已通过普通导入和
`FAST_LOAD=Y` 回灌。

`phase_length` 是 `uint32`，但多 phase 已解决单个超大字段的连续输出问题。超过 4 GiB
整表、`FILESIZE/FILENUM` 多文件索引和单字段超过 4 GiB 的组合仍需继续验证。

## 6. 行字段编码

每行不包含独立行长，`dimp` 根据字段数和表结构顺序读取字段。

### 6.1 通用长度前缀

```text
u16 0xFFFD                         SQL NULL
u16 0xFFFE + u64 length + bytes   LOB/长字段
u16 length + bytes                普通字段，length 可为 0
```

`length=0` 是空字符串/空值内容，与 `0xFFFD` NULL 不同。

### 6.2 已确认类型

| 数据类型 | DMP 内容 |
| --- | --- |
| BIT/BYTE/TINYINT/SMALLINT/INT/BIGINT | 十进制 ASCII 文本 |
| DECIMAL/NUMBER | 十进制 ASCII 文本；例如 `123.45`、`-9.5` |
| REAL/FLOAT/DOUBLE | 十进制或科学计数法 ASCII 文本 |
| CHAR | 源数据库字符集字节，补齐到定义长度 |
| VARCHAR/VARCHAR2/TEXT/CLOB | 源数据库字符集编码后的字节 |
| BINARY/VARBINARY | 小写十六进制 ASCII 文本，不带 `0x` |
| DATE | 长度 6，依次为小端 `uint16 year/month/day` |
| TIME | 长度 6，依次为小端 `uint16 hour/minute/second`；不保存小数秒 |
| TIME WITH TIME ZONE | 含小数秒和时区的 ASCII 文本 |
| TIMESTAMP(6) | 长度 16：6 个小端 `uint16` 年月日时分秒，加小端 `uint32 nanosecond` |
| TIMESTAMP WITH LOCAL TIME ZONE | 与 TIMESTAMP 相同的 16 字节结构 |
| TIMESTAMP WITH TIME ZONE | 含小数秒和时区的 ASCII 文本 |
| INTERVAL YEAR TO MONTH | 规范化 INTERVAL ASCII 文本 |
| INTERVAL DAY TO SECOND | 规范化 INTERVAL ASCII 文本 |
| ROWID | ROWID 显示值的 ASCII 文本 |
| CLOB | `0xFFFE + uint64 length + 字符数据` |
| BLOB | `0xFFFE + uint64 length + 原始二进制` |

`TIME(6)` 有一个已经复现的格式限制：原生 `dexp` 只输出时、分、秒，官方
`dimp` 回灌后 `.123456` 会变成 `.000000`。进一步实验结果：

- 5 字节物理 packed TIME 被 `dimp` 拒绝；
- `23:59:58.123456` 文本被拒绝；
- 10 字节“时分秒 + 小数秒”可以导入，但后 4 字节被忽略。

因此当前 DM 构建的 DMP 通道无法无损保存 TIME 小数秒。DMP 导出必须记录明确告警，
不能假装已经完整恢复。BFILE、复杂类型、自定义类型和集合类型尚未形成编码结论。

DM 默认模式下空字符串会成为 SQL NULL；本次样例中空 VARCHAR 的 DMP 字段也是
`0xFFFD`，不是长度 0。写入器仍保留长度 0 与 NULL 两种格式，以兼容其他模式或类型。

## 7. footer 索引

footer 固定以以下 8 字节开始：

```text
9B A0 78 C6 D5 0C F2 85
```

未压缩单 schema 样例的索引结构为：

```text
magic[8]
u16 reserved
u64 schema_record_offset
string16 schema_name
string16 owner_name

repeat table:
    u32 marker = 0x9CD81630
    u16 reserved
    u64 metadata_offset
    u32 table_object_id
    string16 table_name
    u32 phase_count
    repeat phase:
        u16 reserved
        u64 phase_offset
```

`string16` 是 `u16 length + bytes`。footer 中保存所有 phase 的绝对文件偏移；表总行数
应累计每个数据 phase 的非 `0xFFFFFFFF` 行数，不能只读取 phase-2。压缩基础类型表和
超大 LOB 都可能在 phase-3 以后才完成最后一批行。

多 schema 和跨文件 footer 尚未完成验证。

## 8. 压缩

`COMPRESS=Y` 时并不是把整个 payload 压成一个流，而是对名称、DDL、行块和 footer 字符串等可变项分别使用 zlib：

```text
u16/u32 compressed_length
byte zlib_stream[compressed_length]
```

压缩级别 1 的 zlib 头常见为 `78 01`，级别 9 常见为 `78 DA`。125 MiB 的重复 LOB
样例在 9 级压缩后约为 202 KiB，但仍保留 14 个 phase，说明压缩以原始 phase 边界为
基础分别进行。当前 Go 写入器只生成未压缩 DMP；先保证兼容性，再考虑逐 phase 压缩。

加密格式尚未研究。官方说明加密时会先压缩再加密，因此不能沿用未压缩数据块的直接解析方式。

## 9. 官方工具回灌验证

以下实验均已在隔离测试用户中完成：

1. 等长修改第一行文本，重算 MD5 和头异或校验；`dimp CTRL_INFO=4` 通过，导入后查询得到修改后的文本。
2. 手工追加第 4 行，更新 phase-2 长度、行数和 `payload_end`；NULL、VARCHAR、DECIMAL、DATE、TIMESTAMP、VARBINARY 全部导入正确。
3. 删除 CREATE TABLE、注释和索引记录，生成对象数为 0 的最小纯数据 DMP；`SHOW=Y` 仍能显示 3 行，并可 `DATA_ONLY=Y FAST_LOAD=Y` 导入。
4. 使用 Go `DMPDataWriter` 从零生成两行 DMP；官方 `SHOW`、MD5 校验和快速装载全部成功，查询结果与输入一致。
5. 先导入 `ROWS=N` DMP 建表，再导入纯数据 DMP，验证 DDL 与数据可以分离恢复。
6. 在独立 GB18030 和 EUC-KR 实例中回灌 25 列基础类型、ROWID、RANGE/LIST/HASH
   分区和 125 MiB LOB；除 TIME 小数秒外，未压缩和 9 级压缩结果均与源数据一致。
7. 用 Go 生成 GB18030/EUC-KR DMP，官方分别显示 `PG_GB18030` 和 `PG_EUC_KR`，
   MD5 校验、普通导入和快速装载均通过。
8. 用 Go Reader 流式生成 8 MiB 分段的 16 MiB CLOB 字节流加 16 MiB BLOB；
   普通导入和 `FAST_LOAD=Y` 都完整提交，长度与首尾字节一致。
9. 纯数据分区表 DMP 回灌后，4 行分别进入 4 个 RANGE 分区。
10. 正式 `unload table` 从 oldpro 离线 DBF 生成 `T_LOB_TEST` 与 `T_ORDER_RANGE`
    DMP；官方 `dimp DATA_ONLY=Y FAST_LOAD=Y REMAP_SCHEMA` 无告警导入 10/10 行。
    LOB 汇总为 CLOB 326 字符、BLOB 141 字节；RANGE 四个分区行数为 3/2/2/3。

### 9.1 字符集边界

DMP 字符字段保存源数据库编码的原始字节。当前构建不会把表数据可靠地自动转换为
目标数据库字符集：

- EUC-KR DMP 导入 GB18030、GB18030 DMP 导入 EUC-KR 时，字符码点发生变化；
- 两种 DMP 导入 UTF-8 实例时，基础表报 `String truncated`，字符数据 0 行；
- 同字符集导入和按目标字符集重新编码后导入均正确。

因此 DMP 输出字符集必须与目标库字符集一致。离线恢复时应使用 `bootstrap` 识别出的
字符集编码字段名、字符列和 CLOB，同时写入配套的 `0x115/0x43E` 头字段。跨字符集恢复
应先转码后生成目标字符集 DMP，不能把原始源编码字节直接换一个头标志。

推荐校验命令：

```bash
# 只查看内容
dimp USER/PASSWORD FILE=table_data.dmp SHOW=Y NOLOGFILE=Y

# 只校验 payload MD5，不执行导入
dimp USER/PASSWORD FILE=table_data.dmp CTRL_INFO=4 NOLOGFILE=Y

# 目标表已由 DDL 创建后快速装载
dimp USER/PASSWORD FILE=table_data.dmp \
  DATA_ONLY=Y TABLE_EXISTS_ACTION=APPEND FAST_LOAD=Y CTRL_INFO=2
```

## 10. dmdul 中的阶段性实现

代码位于：

- `internal/dm/dmp.go`
- `internal/dm/data_dmp.go`
- `internal/dm/dmp_test.go`

已实现：

- 4KB 头解析；
- 逻辑版本、模式、压缩、行格式标志和对象数识别；
- payload MD5、头 XOR、footer magic 校验；
- 单 schema footer/table/phase/行数解析；
- UTF-8、GB18030、EUC-KR 双头字段识别与写入；
- 按文件字符集编解码 schema/table 名；
- 多 phase 行数累计和 `0xFFFFFFFF` 续传识别；
- 未压缩、表级、纯数据 DMP 的流式写入；
- NULL、短字段、64 位长度长字段和 Reader 流式字段写入；
- 8 MiB 多 phase 自动切分，支持超大行和 LOB 跨 phase 续传；
- 写完后回填各 phase 长度、行数、footer、MD5 和头校验；
- `set data_format dmp` 已接入表级、用户级和整库级 `unload`；
- 用户级和整库级按表生成 DMP，空表不生成空文件；
- 从活动行 locator 出发流式读取 `0x20` LOB 页链；
- DMP 写入器可暂停和继续，限制整库导出时同时打开的文件数；
- 基础类型按已验证矩阵编码，TIME 小数秒丢失会输出显式告警；
- 长字段长度和文件偏移使用 64 位，超过 4 GiB 的整表通过多 phase 输出。

没有新增交互式 DMP 检查命令。DMP 定位为“SQL DDL + 每表一个纯数据 DMP”，
默认 `data_format` 仍为 `sql`。

## 11. 正式导出方式

当前采用“SQL DDL + 每表一个纯数据 DMP”：

```text
HR_TEST_EMP_INFO_ddl.sql
HR_TEST_EMP_INFO_data.dmp
```

恢复顺序：

1. 执行 DDL，创建用户、表、约束和必要对象；
2. 对每张表执行 `dimp DATA_ONLY=Y FAST_LOAD=Y`；
3. 数据装载完成后创建或重建二级索引、外键和触发器；
4. 做行数、MD5 和抽样内容校验。

这样既避开逐行 `INSERT INTO`，也不必在第一版就生成用户、视图、包、权限等全部 DMP 对象记录。

仍需继续研究和验证：

- 补齐 BFILE、复杂类型、自定义类型和集合类型编码矩阵；
- 原生多文件 footer、压缩和加密 DMP；
- 单个 21 字节 locator 的长度字段是 32 位；超过该范围的其他 LOB locator 形态尚未确认；
- 迁移行、链式行和部分恢复行的列值策略；
- 不同 DM8 构建版本及 `FILE_VERSION` 兼容性测试；
- 导入失败时的错误定位和逐表重试清单。

建议先在隔离测试库执行 `dimp CTRL_INFO=4` 校验，再使用 `DATA_ONLY=Y FAST_LOAD=Y`
装载。跨字符集恢复必须按目标字符集重新生成 DMP，不能只修改文件头标记。
`case_sensitive=auto` 从 `SYSTEM.DBF` 第 4 页偏移 `0x2C` 读取建库标志，并写入
DMP 头 `0x435`。该对应关系已通过 6 个已有实例及一组“同字符集、仅
CASE_SENSITIVE 不同”的差分实例验证。只有控制页损坏或无法识别时，才需要通过
`set case_sensitive 0|1` 显式覆盖，否则 `dimp` 可能等待人工确认。
