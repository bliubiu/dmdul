# 安装方式

## 环境要求

- 运行发布包不需要安装 Go；从当前源码构建需要 Go 1.26 或更高版本，建议使用最新补丁。
  历史 tag 的构建要求以对应 `go.mod` 为准，例如 v0.10.0 仍为 Go 1.22。
- Windows/Linux 有自动化测试与构建检查；macOS 可交叉构建，但不在当前 CI 运行矩阵中。
- 常规字典模式需要：
  
  - `SYSTEM.DBF`
  -  用户数据所在表空间文件，例如 `MAIN.DBF`、`TBS_*.DBF`
  
  可选但强烈建议提供：
  
  - `dm.ctl`：用于补充数据库名、表空间名和数据文件路径。

SYSTEM 字典不可用时可使用 [无字典救援](storage-rescue.md)，但必须人工提供可靠列结构，
不能保证恢复原始对象名称。

## 从源码构建

在项目根目录执行：

```powershell
go test ./...
go build -o .\bin\dmdul.exe .\cmd\dmdul
```

查看版本：

```powershell
.\bin\dmdul.exe version
```

查看帮助：

```powershell
.\bin\dmdul.exe help
```

## Linux 构建示例

```bash
go test ./...
go build -o ./bin/dmdul ./cmd/dmdul
./bin/dmdul help
```

## 交叉编译示例

在 Windows 上构建 Linux x64：

```powershell
$env:GOOS="linux"
$env:GOARCH="amd64"
go build -o .\bin\dmdul-linux-amd64 .\cmd\dmdul
```

恢复当前 PowerShell 会话的默认构建环境：

```powershell
Remove-Item Env:\GOOS
Remove-Item Env:\GOARCH
```

## 发布版本构建

使用 [开发说明中的发布构建流程](development.md#发布构建)，从 HEAD 的精确 tag 注入版本、
提交号和 UTC 时间，并打包许可证。不要使用最近的历史 tag 为未发布源码命名。

## 安全建议

- 不建议在源码仓库中保存生产库文件。
- 导出的 SQL 可能包含业务数据，应按敏感数据处理。
- 建议在隔离目录中放置待解析文件，并只把工具源码上传到 GitHub。
