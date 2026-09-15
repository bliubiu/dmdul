# Hardening：规则、质量检查与实测

本页描述 v0.11.0 的加固，日期为 2026-09-15。v0.10.0 发布包不包含这些修改；
历史实测保留在 [v0.10.0 发布验证](release-v0.10.0-validation.md)，不回改旧标签。

## 页大小探测

`ProbePageSize` 共用于文件发现、字典流和无字典救援的几何探测。它只读取源文件，
不修复、不回写页头，也不把页大小识别成功当成整个文件健康。

| 规则 | 当前行为 |
| --- | --- |
| 支持候选 | 4、8、16、32 KiB；64 KiB 不作为支持格式 |
| 采样 | 每种候选的前 128 个非零页号，加 32 个分布点；排序、去重，仅读取完整页 |
| I/O 上限 | 四种候选合计最多 640 个页读取，加一次 256 字节文件头；总量最多 9,830,656 字节 |
| 身份证据 | 页内 page number 等于物理位置，group/file 合法，页类型在已观察范围内 |
| 结构证据 | 同一 group/file 至少三页吻合，并至少一页通过 CRC/HASH 或行结构校验 |
| 损坏页头 | 必须唯一确定候选；不能仅因文件长度是 8 KiB 的倍数就选择 8 KiB |
| 有效页头 | 没有矛盾的充分证据时保留页头值；允许无已分配数据页的小文件 |
| 冲突 | 页头与物理证据矛盾、多个页大小成立、或同一页大小有多个充分 group/file 候选时停止 |
| 读取错误 | 采样短读或 I/O 错误直接返回，不能被静默忽略后变成“探测成功” |
| 页数边界 | 完整页数超过 uint32 表示范围时停止，不截断为较小的页数 |

文件尾部不是整页时，探测可以返回有效页头中的页大小，让物理检查报告截断；
这不表示截断文件已经可用。样本不足而页头也无效时必须停止，不能声称自动修复。
合法页头但没有足够物理证据的返回值仍是“页头值”，并非独立证实。
冲突表示不能安全自动选择，不等于全部数据已损坏；残留旧身份也可能产生冲突，需人工核对文件来源。

ASM 候选数据库列表等轻量元数据读取保留文件头快速路径，不执行完整探测；
实际打开字典流时才做冲突检查。不要把启动时打印的默认参数或候选信息当作校验结论。

### 冷文件复测

`TestPageGeometryOfflineSamples` 只读用户给出的冷 `SYSTEM.DBF`。错误字段由 `ReaderAt`
内存覆盖模拟，不修改磁盘字节；自动 CI 未提供路径时跳过该集成测试，不跳过合成测试。

```powershell
$env:DMDUL_GEOMETRY_SAMPLES = 'D:\cold-dm8\SYSTEM.DBF;D:\cold-dm9\SYSTEM.DBF'
try {
    go test ./internal/dm -run '^TestPageGeometryOfflineSamples$' -count=1 -v -timeout=5m
} finally {
    Remove-Item Env:DMDUL_GEOMETRY_SAMPLES
}
```

Linux 使用 `:` 分隔多个路径。只传冷副本，不对在线 DBF 的探测结果承诺一致性。

## CI 与 Fuzz

质量工作流固定第三方 Action 的提交号，只使用仓库内的合成或脱敏样本，不连接私有测试机。
Windows/Linux 分别在 Go 1.26 最新补丁与 stable 下运行格式、模块完整性、vet、测试和构建。
Linux 另跑 race；手动 fuzz 可选择 30 或 300 秒，默认每组 30 秒、两个 worker。

| Fuzz 入口 | 断言重点 |
| --- | --- |
| FuzzPageGeometry | 任意短输入不会产生非法页大小 |
| FuzzPageGeometryFields | 紧凑字段构造多页证据，识别一致候选、拒绝矛盾，不修改输入 |
| FuzzPageProbeRefs | 极端文件长度和页大小下，采样有界、有序、不重复、不越界 |
| FuzzPageCheck | SM3/SHA256 sector 页破坏检测、校验只读、保护字节还原幂等 |
| FuzzDataPageSlots | 行跨度不越界，普通 unload 不读取删除 slot 或物理空洞 |
| FuzzDataRowMetadata | NULL metadata、行尾、Undo 公共头的长度边界 |
| FuzzDMPContainer | 截断、错误长度、头尾元数据不会导致解析崩溃 |
| FuzzDMASMMetadata | INODE/副本描述安全解析、不修改输入、文件长度不变为负数 |
| FuzzHugeCompressedBody | ZIP/Snappy 长度约束、截断和非法载荷；解压后大小有界 |

失败时保存 Go 生成的最小语料七天。修复后将可公开的最小输入放入对应 `testdata/fuzz/`，
使普通 `go test` 也执行回归。不要提交生产 DBF、业务内容、私钥或密码。
短时 fuzz 不能证明格式完全覆盖，也不能替代官方工具导回比对。

工作流更新提交前不能声称远端 CI 已通过；分支保护仍需维护者在 GitHub 设置中配置，
工作流不会自动阻止直接推送。

## 安全依赖

当前 `x/text v0.42.0` 要求 Go 1.26，CI 与源码安装说明同步提高最低版本；
旧版 Go 1.22 不再是当前分支的编译承诺。Go 只影响源码构建，发布包使用者无需安装。
SM3 继续引用 `tjfoc/gmsm v1.4.1` 的纯 Go `sm3` 包；许可证随包保留。

旧 `x/text v0.22.0` 涉及 [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970)：
`unicode/norm` 处理非法输入可能死循环，v0.39.0 起修复。旧 dmdul 没有调用受影响符号，
但模块公告仍存在；本轮通过升级移除该公告，而不是设置忽略规则。
[x/text v0.42.0](https://pkg.go.dev/golang.org/x/text@v0.42.0) 的许可证与仓库副本 SHA256 一致。

CI 同时运行 Windows/Linux 调用路径分析与模块扫描。模块扫描发现已知公告即失败，
不以“当前没有调用”为由放行旧依赖。工具命令与发布构建流程统一维护在
[开发说明](development.md#依赖与漏洞检查)。

## 实测记录

测试只操作已有隔离实例与独立输出目录，两个主实例保持运行。没有创建第二个 DM9 build，
没有新增对 64 KiB、事务提交视图或 ASM REDO 的支持声明。

- DM8 build：`03134284336-20250117-257733-20132`。
- DM9 build：`03151060506-20260417-322930-20218`。

| 项目 | 2026-09-15 结果 |
| --- | --- |
| DM8 冷 SYSTEM，8 KiB | 原始头、无效值恢复、三种合法但错误页大小拒绝，通过 |
| DM9 八份冷 SYSTEM | 4 KiB UTF-8 / GB18030 / EUC-KR，PAGE_CHECK 0/1/3；8/16/32 KiB UTF-8 SM3，通过 |
| 读取量 | 每份八次字段组合实验，最多 641 次读取 / 9,830,656 字节；源 DBF 不修改 |
| Windows Go 1.26.8 / 1.27.1 | 完整测试、vet、构建通过 |
| 普通测试覆盖率 | Go 1.27.1：internal/dm 65.3%，internal/cli 70.7%；不含私有冷样本入口 |
| Linux Go 1.26.8 | 交叉编译的 dm/cli 测试在 DM8 测试机运行通过 |
| Linux Go 1.27.1 | vet、完整 race、八组各 30 秒 fuzz、构建通过 |
| 编码已知字节 | UTF-8、GB18030 双/四字节、EUC-KR 解码与 DMP 编码通过 |
| CI 配置 | actionlint 本地检查通过；工作区尚未推送，远端新工作流未执行 |
| Go 1.27.1 漏洞扫描 | Windows/Linux 调用路径及模块级扫描均无已知漏洞报告 |
| DM8 HUGE SQL 导回 | 2500 行，双向 MINUS 为 0；nullable INT/BIGINT 非 NULL 数分别为 1667/1875 |
| DM8 权限 DMP 导回 | 三项列级权限一致；三项系统权限及 ADMIN OPTION 一致，用户表 ID=7 |
| DM9 八组导出/检查 | 每组 3 行，SQL/fldr/DMP 导出均 0 失败；SYSTEM 与全文件集检查均 0 坏页 |
| DM9 八组 DMP 导回 | 官方 dimp 导入隔离目标模式后，双向 MINUS 均为 0 |

DM9 八组分别为：4 KiB UTF-8 的 PAGE_CHECK 0/1/3，4 KiB GB18030 / EUC-KR 的
PAGE_CHECK 3，以及 8/16/32 KiB UTF-8 的 PAGE_CHECK 2 + SM3；均为 CASE_SENSITIVE=0。
本轮 fldr 验证到文件生成，未重复执行 dmfldr 装载；不要把三通道导出都写成三通道导回。
所有隔离实例最终正常关闭，DM8/DM9 原主实例进程保持运行。

## 保留的边界

本轮加固不等于实现 `committed-only`。完整 Undo 前镜像、普通迁移行/链式行、第二个 DM9 build、
DMASM REDO 和删除文件救援仍未完成。同日后续新增的 HUGE ZIP/Snappy、多 HFS path 与
对象定义恢复及九组 fuzz 结果见 [扩展兼容验证](extended-compatibility-20260915.md)。
