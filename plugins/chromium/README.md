# Chromium compatibility directory

ELIZA now prefers `./tools` alongside the binary for optional headless browser assets. This
legacy directory is still scanned for compatibility when the binary runs from a
repo or release bundle.

Supported layouts include:

- `chrome-linux64/chrome`
- `chrome-linux-arm64/chrome`
- `chrome-headless-shell-linux64/chrome-headless-shell`
- `chrome-headless-shell-linux-arm64/chrome-headless-shell`
- `chromium`
- `chrome`

You can also set `ELIZA_BROWSER_EXEC_PATH` to point directly at a browser
executable.
