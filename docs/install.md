# 安装方式

## 环境要求

- Go 1.22 或更高版本。
- Windows、Linux、macOS 均可编译；当前样例和命令主要在 Windows PowerShell 下验证。
- 离线抽取至少需要：
  
  - `SYSTEM.DBF`
  -  用户数据所在表空间文件，例如 `MAIN.DBF`、`TBS_*.DBF`
  
  可选但强烈建议提供：
  
  - `dm.ctl`：用于补充数据库名、表空间名和数据文件路径。

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

可以通过 `-ldflags` 写入版本号和提交号：

```powershell
$commit = git rev-parse --short HEAD
$tag = git describe --tags --abbrev=0
go build -trimpath -ldflags "-s -w -X dmdul/internal/version.Version=$tag -X dmdul/internal/version.Commit=$commit" -o .\bin\dmdul.exe .\cmd\dmdul
```

## 安全建议

- 不建议在源码仓库中保存生产库文件。
- 导出的 SQL 可能包含业务数据，应按敏感数据处理。
- 建议在隔离目录中放置待解析文件，并只把工具源码上传到 GitHub。
