# Frontend behavioral tests

From the repository root, with Node.js 22 or later:

```sh
node --experimental-vm-modules --test --test-timeout=10000 tests/*.test.mjs
```

No npm installation, generated Wails bindings, or running desktop application is
required. The VM-modules experimental warning is expected. CI runs this command
on Windows, macOS, and Linux.

The tests execute the real frontend modules with explicit backend, DOM, history,
and canvas doubles. Deferred promises control response ordering; scan timers are
triggered explicitly, without real-time sleeps.

- `navigation.test.mjs`: back/forward navigation, branching history, stale scan
  sessions, removed history entries, invalid selections, and failed toggles.
- `scan.test.mjs`: cached-result freshness, cancellation, duplicate requests,
  cleanup, validation errors, and progress responses arriving after completion
  or after a new scan starts.
- `treemap-view.test.mjs`: out-of-order layout responses, navigation/resize/scale
  changes, invalid responses, and preservation of the last displayed view.
- `file-actions.test.mjs`: confirmation and cancellation, target retention,
  duplicate submissions, protected selections, failures, rescanning, and restore.

These are module-level behavioral tests, not browser end-to-end tests. They do
not validate pixel output, browser history implementation, accessibility layout,
or native operating-system dialogs. Keep those covered by desktop smoke tests
and the platform-specific Go tests.
