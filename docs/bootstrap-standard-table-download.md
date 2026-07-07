# DM8 Bootstrap 与标准表下载优化设计笔记

本文记录 DMDUL 后续优化 bootstrap 和表数据下载时采用的主路径设计。

## 核心结论

`SYSOBJECTS`、`SYSINDEXES` 中记录的 storage root 应作为访问表或索引的标准入口。root 页不是数据页并不代表地址不可靠，通常只是说明还需要继续解析 BTree root/internal 结构。

当前优先路径应为：

```text
storage root
  -> root/internal page，page_kind = 0x15
  -> child pointer，已观测偏移 0x52
  -> more internal page(s)，page_kind = 0x15
  -> first leaf/data page，page_kind = 0x14
  -> leaf next-page chain，页头偏移 0x0e
```

因此，正常表下载不应把全文件扫描作为主路径。全文件扫描只能作为损坏、缺字典、root 链断裂时的救援兜底。

## Page Plan 优先级

生成 page plan 时按以下顺序：

1. 如果字典文件中已有明确的 `page_refs` 或 `page_numbers`，优先使用，并逐页校验页头。
2. 如果字典中有 `storage_id`、`root_file`、`root_page`：
   - 读取 root 页；
   - 校验页头 `(group_id, file_id, page_no)` 和 `storage_id`；
   - root 是 `0x14` 时，直接沿 leaf next 链读取；
   - root 是 `0x15` 时，按 `0x52` 左孩子下降；
   - 如果左孩子不可读或页头校验失败，尝试沿 internal 页自己的 `0x0e` next 链找同层 internal 页；
   - 找到第一个 `0x14` 后，再沿 leaf next 链读取数据页。
3. 上述结构解析失败后，才进入 root 附近的局部扫描。
4. 全文件 `storage_id` 扫描是最后兜底，主要用于损坏文件或未知结构。

当前代码中，普通表数据导出已实现 `storage root -> internal -> leaf chain`，并已补充 internal next fallback。

## Bootstrap 优化方向

长期目标是让 bootstrap 也尽量走“标准表下载”，而不是长期依赖对 `SYSTEM.DBF` 的全文件字节流扫描。

目标流程：

```text
SYSTEM.DBF page 0 bootstrap-like 入口
  -> 定位 SYSOBJECTS root
  -> 按标准表下载 SYSOBJECTS
  -> 从 SYSOBJECTS 中定位 SYSUSERS / SYSCOLUMNS / SYSINDEXES / SYSHPARTTABLEINFO 等字典对象
  -> 对每张字典表继续按标准表下载
  -> 生成 dmdul_dict 文本字典
```

已观测入口：

```text
SYSTEM.DBF page 0 + 0x80: SYSOBJECTS root page
SYSTEM.DBF page 0 + 0x7c: SYSINDEXES root page
```

读取 root 页后，应从页头取得真实 `storage_id`，再走标准 BTree 下载路径。固定 root page、固定 storage id 和字符串 marker 扫描都只能作为兼容兜底。

## 灾难恢复模式边界

当 `SYSTEM.DBF` 丢失、核心字典入口不可读，或字典页严重损坏时，普通 `bootstrap` 不应悄悄生成空字典并继续导出。应返回明确错误并写入日志。

可以单独设计显式救援模式，例如：

```text
bootstrap --scan-storages-without-system-dicts
```

该模式只扫描 DBF 页头，按 `(group_id, file_id, storage_id)` 聚合 `page_kind=0x14` 的数据页，生成类似 `storage_scan.dict` 的物理字典。它不能恢复真实 owner、表名、列名和类型，只用于先保全 raw row bytes，后续再结合人工字典或残留字典结构化恢复。

## 后续任务

- 将 bootstrap 的 SYSOBJECTS 入口定位改为优先读取 page 0 的 `0x80`。
- 为 SYSOBJECTS / SYSINDEXES / SYSCOLUMNS 等核心字典表复用标准 page plan 下载。
- 增加 bootstrap diagnostics，记录入口页、storage id、root 页、leaf 链长度和 fallback 原因。
- 将 marker 扫描降级为明确的 fallback，并在日志中标注。
- 设计独立的 raw storage scan 救援模式，避免和正常字典恢复混在一起。
