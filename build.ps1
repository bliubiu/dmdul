$ErrorActionPreference = "Stop"

$version = git describe --tags --abbrev=0
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($version)) {
  $version = "dev"
}

$commit = git rev-parse --short HEAD
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($commit)) {
  $commit = "unknown"
}

$time = Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"
$ldflags = "-s -w -X dmdul/internal/version.Version=$version -X dmdul/internal/version.Commit=$commit -X dmdul/internal/version.BuildTime=$time"

go test ./...
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}

New-Item -ItemType Directory -Force -Path .\bin | Out-Null

go build `
  -trimpath `
  -ldflags $ldflags `
  -o .\bin\dmdul.exe `
  .\cmd\dmdul
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}

Compress-Archive `
  -Path .\bin\dmdul.exe `
  -DestinationPath ".\bin\dmdul_windows_amd64_$version.zip" `
  -Force

Get-FileHash ".\bin\dmdul_windows_amd64_$version.zip" -Algorithm SHA256
