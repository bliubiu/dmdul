# v0.11.0 发布验证

日期：2026-09-15。主题：Hardening & Extended Recovery。

本版同时发布解析加固和已经完成导回验证的扩展能力，不表示整个路线图已完成。
源码构建最低要求由 Go 1.22 提升为 Go 1.26；发布包使用 Go 1.27.1，使用者无需安装 Go。

## 发布范围

- 页大小采用确定性有界采样，拒绝短读、冲突文件身份、歧义和页数溢出。
- HUGE ZIP level 2/9、Snappy level 10、两条 HFS 路径及已验证的可空标量。
- 持久化 `types.tsv`、`directories.tsv`；SQL/DMP 恢复类型、类型体、全局目录与简单物化视图定义。
- 九组 parser fuzz、依赖完整性、Windows/Linux 测试与漏洞门禁；x/text 升级至 v0.42.0。

## 实验结果

| 项目 | 结果与边界 |
| --- | --- |
| DM8/DM9 冷 SYSTEM | 九份样本的原始与模拟损坏页头通过；不修改源文件 |
| DM9 参数矩阵 | 八组，覆盖 4 KiB、CASE_SENSITIVE=0、三种字符集、PAGE_CHECK 0/1/3 与 8/16/32 KiB SM3；DMP 导回双向 MINUS 为 0 |
| HUGE SQL | 16 张表、48,500 行，回灌双向 MINUS 为 0，包含压缩、NULL、标量和双路径 |
| 扩展对象 | 类型/类型体与四种简单物化视图 SQL、同模式 DMP 导回通过；目录原生记录导入路径一致 |
| Windows | Go 1.26.8 / 1.27.1 完整测试通过；Go 1.27.1 vet、漏洞调用路径及模块扫描通过 |
| Linux | Go 1.27.1 vet、完整 race、九组各 30 秒 fuzz 通过 |
| CI 配置 | actionlint 本地检查通过；远端运行结果应按发布提交在 GitHub Actions 查询，不以本地结果代替 |

DM8 build 为 `03134284336-20250117-257733-20132`，DM9 build 为
`03151060506-20260417-322930-20218`。不能将单个 DM9 build 的结果外推为全部版本兼容。
实验实例已关闭，原有主实例未停止。详细记录见 [Hardening](hardening.md) 和
[扩展兼容验证](extended-compatibility-20260915.md)。

## 升级注意

1. 保留已有手工修订字典的备份；需要新增对象信息时，对一致冷快照重新执行 `bootstrap;`。
2. 旧字典缺少物化视图标志时不猜测定义；元数据不足时 SQL 保留告警，DMP 停止。
3. 简单物化视图统一 `BUILD DEFERRED`，不恢复原始构建状态、物化行数据、日志和刷新任务。
4. 跨模式恢复需人工检查源码内的限定模式名；不为绕过依赖自动增加授权。
5. 自定义类型定义不代表该类型列的数据已能解码；目录对象不创建操作系统目录。

## 未完成项

- 完整 Undo 与 committed-only、普通迁移行拼接。
- 第二个不同 build 的 DM9；实验 build 不接受 64 KiB 页和 4 KiB HASH 初始化。
- HUGE DECIMAL 专用打包、完整 HFS 校验和、DMASM HFS；新增标量的逐类型 DMP/dmfldr 回灌仍需补充。
- DMASM 单文件超过 65535 AU、REDO 重放及已删除 ASM 文件救援。
- 复杂物化视图、作业、策略及完整依赖排序。

## 发布产物

Windows amd64 ZIP 和 Linux amd64 tar.gz 均从精确 tag 构建，注入版本、提交号、UTC 构建时间，
使用 `-trimpath -s -w`。每个包必须包含 `LICENSE`、`THIRD_PARTY_NOTICES.md` 和 `licenses/`
中的 Go、x/text、gmsm、Snappy 四份许可证。校验和随发布资产提供；不得将实验快照、原生对照
DMP 或含凭据的临时脚本打入源码提交或二进制包。
