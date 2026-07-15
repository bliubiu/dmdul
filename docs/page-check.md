# DM8 PAGE_CHECK 页校验实验

本文记录 2026-07-15 在独立 DM8 实例上对 `PAGE_CHECK=0/1/2/3` 的页级差分结果。实验使用相同
的 8 KiB 页、表结构和三条数据，并在 `CHECKPOINT(100)` 和正常关闭后读取同一 `MAIN.DBF` page 16。

达梦官方定义：模式 0 禁用校验；模式 1 使用标准 CRC32；模式 2 使用 `PAGE_HASH_NAME` 指定的
HASH；模式 3 使用快速 CRC32C。参考：[dminit 参数详解](https://eco.dameng.com/document/dm/zh-cn/pm/dminit-parameters.html)。

## CRC 模式

模式 1 和模式 3 的校验值都位于页头 `0x18..0x1B`，按 little-endian `u32` 保存。计算步骤为：

```text
work_page = raw_on_disk_page
work_page[0x18:0x1C] = 00 00 00 00
payload = work_page[0 : page_size - 8]

PAGE_CHECK=1: checksum = CRC32/IEEE(payload)
PAGE_CHECK=3: checksum = CRC32C/Castagnoli(payload)
```

实机 8 KiB 样本：

| 模式 | 页头存储值 | 独立重算 |
| ---: | ---: | ---: |
| 0 | `0x00000000` | 不校验 |
| 1 | `0xF3B8E936` | CRC32=`0xF3B8E936` |
| 3 | `0x6EA5FEFA` | CRC32C=`0x6EA5FEFA` |

模式 0、1、3 的目标页除 `0x18..0x1B` 外完全一致，因此该字段定位不是相关性猜测。

## HASH 模式

实验使用 `PAGE_CHECK=2 PAGE_HASH_NAME=SHA256`，在线参数返回：

```text
ENABLE_PAGE_CHECK = 2
PAGE_CHECK_ID     = 2304
CYT_NAME          = SHA256
```

HASH 不写入 `0x18`。其位置和输入范围由摘要长度决定：

```text
hash_offset = page_size - digest_size - 8
stored_hash = page[hash_offset : hash_offset + digest_size]
calculated  = HASH(page[0 : hash_offset])
```

8 KiB + SHA256 的 `hash_offset=0x1FD8`，实机保存值与
`SHA256(page[0:0x1FD8])` 完全一致。HASH 占用页尾空间，因此 slot/空闲边界会相应前移。

对应 slot 目录公式为：

```text
slot_start = page_size - digest_size - 8 - n_slot * 2
```

固定使用 `page_size - 8 - n_slot * 2` 会把 SHA256 摘要误读为 slot。DMDUL 的 SYSTEM 字典页、
分区字典页和用户数据页现已统一使用 HASH 感知的 slot 起点。

## DMDUL 实现

`internal/dm/page_check.go` 已实现：

- CRC32/IEEE；
- CRC32C/Castagnoli；
- MD5、SHA1、SHA224、SHA256、SHA384、SHA512 页 HASH；
- 校验值不匹配和单字节损坏测试。

Go 标准库没有当前项目可直接使用的 SM3 实现，因此 `SM3/OPENSSL_SM3` 会明确返回“不支持”，不会
误用其他算法。对于摘要损坏或 SM3 无法重算的页面，DMDUL 仍会根据 slot 目录结构在
`16/20/28/32/48/64` 字节候选中保守推断摘要长度，以便救援模式继续定位 slot；该推断不等于校验
通过。页校验失败不应自动丢弃救援数据，后续接入导出日志时应分别记录“校验失败”和“仍尝试恢复”。

实机回归还直接使用模式 0 和 SHA256 模式的完整 `SYSTEM.DBF`、`MAIN.DBF` 执行当前 DMDUL。两者的
标准两阶段 bootstrap 都恢复 `1063` 个对象，随后对 `SYSDBA.PC_T` 生成一个计划页、直接读取一个页、
无 fallback，三条数据全部导出且字段值与在线插入值一致。这既验证了 HASH 尾部处理，也验证了
模式 0 不会被结构推断误判成 HASH 页。
