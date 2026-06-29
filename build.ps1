$version = git describe --tags --abbrev=0
$commit  = git rev-parse --short HEAD
$time    = Get-Date -Format "yyyy-MM-dd HH:mm:ss"

go test ./...

go build `
  -ldflags "-X dmdul/internal/version.Version=$version `
            -X dmdul/internal/version.Commit=$commit `
            -X dmdul/internal/version.BuildTime='$time'" `
  -o bin/dmdul.exe `
  ./cmd/dmdul

Compress-Archive `
  -Path .\bin\dmdul.exe `
  -DestinationPath ".\bin\dmdul_windows_amd64_$version.zip" `
  -Force

Get-FileHash ".\bin\dmdul_windows_amd64_$version.zip" -Algorithm SHA256