# Wails build tool

Install from the repository root:

```sh
go -C tools/wails install github.com/wailsapp/wails/v2/cmd/wails
wails build -trimpath -tags webkit2_41
```

This separate module pins Wails 2.13.0 with `golang.org/x/tools` 0.52.0.
Wails' original 0.44.0 dependency fails during source analysis with Go 1.27.2
(`package "errors" without types`). Keeping build-tool dependencies here avoids
changing the application's dependency graph. Do not add `@v2.13.0` to the install
command: that bypasses this module's dependency selection.

CI tests and releases install the same tool, and validation includes a full
Wails build on Windows, macOS, and Linux.
