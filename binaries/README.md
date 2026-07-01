# ELIZA binaries

Version: v0.9.0

This directory stores prebuilt ELIZA Agent binaries produced by `make build-all`.
The application version is unchanged; all files below are built from the same
source tree.

| File | Target |
| --- | --- |
| `eliza-linux-amd64` | Linux x86_64 |
| `eliza-linux-arm64` | Linux ARM64 |
| `eliza-darwin-amd64` | macOS Intel |
| `eliza-darwin-arm64` | macOS Apple Silicon |
| `eliza-windows-amd64.exe` | Windows x86_64 |

Headless browser support is optional. On first normal startup ELIZA creates
`~/eliza/tools` as the browser tools directory. Put Chromium or
`chrome-headless-shell` under that directory, or set `ELIZA_BROWSER_EXEC_PATH`
to a browser executable. The legacy `./plugins/chromium` directory is still
detected for compatibility.
