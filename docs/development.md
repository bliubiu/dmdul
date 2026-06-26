# 本地开发、测试、构建说明

## 代码结构

```text
cmd/dmdul/          CLI 入口
internal/cli/       命令行解析、参数默认值、控制台输出
internal/dm/        达梦文件解析、DDL 导出、数据导出
internal/storage/   文件检查和十六进制采样
internal/version/   版本字符串
docs/               用户文档和逆向研究笔记
research/           临时实验脚本和研究材料
```

## 常用开发命令

运行测试：

```powershell
go test ./...
```

构建 Windows 可执行文件：

```powershell
go build -o .\bin\dmdul.exe .\cmd\dmdul
```

查看帮助：

```powershell
.\bin\dmdul.exe help
```

## 本地验证建议

建议准备一个不提交到 Git 的样例目录，例如：

```text
oldpro/
  SYSTEM.DBF
  dm.ctl
  MAIN.DBF
  TBS_BIN_TEST01.DBF
```

执行 DDL 导出：

```powershell
.\bin\dmdul.exe export-ddl `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -out oldpro\dm_offline_all.sql
```

执行数据导出：

```powershell
.\bin\dmdul.exe export-data `
  -file oldpro\SYSTEM.DBF `
  -ctl oldpro\dm.ctl `
  -data-dir oldpro `
  -out oldpro\dm_offline_data.sql
```

## 版本信息

默认版本在 `internal/version/version.go` 中：

```text
0.1.0-dev
```

发布构建可以写入版本号和提交号：

```powershell
$commit = git rev-parse --short HEAD
go build -trimpath -ldflags "-s -w -X dmdul/internal/version.Version=v0.1.0 -X dmdul/internal/version.Commit=$commit" -o .\bin\dmdul.exe .\cmd\dmdul
```

## 测试覆盖方向

当前重点测试方向：

- `SYSTEM.DBF` 页大小、字符集、页保护字节。
- `dm.ctl` 控制文件解析。
- 字典表对象、字段、索引、约束解析。
- DDL 类型格式化。
- 数据页 slot array、树表、堆表、行长、NULL、长 varchar、NUMBER、DATE/DATETIME 解码。
- CLI 默认参数和错误提示。

新增一种行格式或数据类型时，建议先在 `internal/dm/data_test.go` 或相关测试文件中添加最小样本，再实现解析逻辑。

## 逆向研究约定

- 临时脚本放在 `research/`。
- 已验证并进入主流程的规则记录到 `docs/offline-system-scan.md`。
- 系统字典字段含义记录到 `docs/system-dictionary-fields.md`。
- 不要把生产库文件、导出 SQL、含密码的配置提交到仓库。

## 发布前检查清单

```powershell
go test ./...
go build -o .\bin\dmdul.exe .\cmd\dmdul
.\bin\dmdul.exe help
.\bin\dmdul.exe version
```

如果有样例文件，再执行：

```powershell
.\bin\dmdul.exe export-ddl -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl -out oldpro\dm_offline_all.sql
.\bin\dmdul.exe export-data -file oldpro\SYSTEM.DBF -ctl oldpro\dm.ctl -data-dir oldpro -out oldpro\dm_offline_data.sql
```
