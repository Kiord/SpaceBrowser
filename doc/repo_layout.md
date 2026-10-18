## Repository layout

- `wails_main.go`: small entry point that embeds resources and starts the app.
- `internal/app/`: application, scanning, treemap logic, and their Go tests.
- `internal/platform/`, `internal/treewatch/`, and other internal packages: OS integration helpers.
- `web/`: frontend HTML, CSS, and JavaScript; generated bindings stay in `web/wailsjs/`.
- `tests/`: frontend behavioral tests and test helpers.
- `assets/`: icons and artwork.
- `tools/`: pinned build tools.
- `packaging/`: installers and archived release scripts.
- `.github/workflows/`: validation and release pipelines.
