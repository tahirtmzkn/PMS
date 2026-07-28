# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

PMS (Ping Monitoring System) is a small Go desktop app, built with the Fyne GUI toolkit
(`fyne.io/fyne/v2`), that continuously pings a user-defined list of IP addresses/devices and
displays live success/fail/total counters in a table, color-coded green (last ping succeeded)
or red (last ping failed). It's a from-scratch rewrite of an earlier PyQt5/Python version
(see git history) — the goals were a nicer default UI and a single installable binary instead
of a Python venv.

## Commands

```
go build -o build/pms .        # build
./build/pms                    # run
go vet ./...                   # static check
go test ./...                  # headless smoke test
./packaging/build-deb.sh 0.1.0 # build dist/pms_0.1.0_amd64.deb
```

**Do not drive the running app with synthetic clicks/keystrokes to test it.** This repo lives on
a desktop that also has live root SSH sessions to network gear on screen; misdirected synthetic
input is dangerous, and the user has said explicitly that they do the interactive testing
themselves. Verify with `go build`, `go vet`, and `go test`, then describe what changed and hand
off.

`smoke_test.go` is the only test: it builds the whole UI against `test.NewApp()` (headless, no
window opens) and exercises add/remove/sort/resize-drag/formatting. It exists so UI changes —
especially custom widgets like `colResizer` — can be checked for panics and logic regressions
without opening a window on the user's screen. Extend it rather than launching the app.

Building requires cgo + OpenGL/X11 dev headers (one-time, Ubuntu 24.04):
```
sudo apt install -y gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev
```

## Architecture

- `device.go` — `Device` struct: one monitored target's IP/name plus running
  success/fail/total counters and `LastResult`.
- `ping.go` — `pingOne` shells out to the system `ping` binary
  (`ping -I <interface> -c 1 -W <timeout_sec> <ip>`). `runCycle` fans a ping out to every device
  concurrently through a size-10 semaphore (`maxConcurrentPings`), calling `onResult` as each
  device finishes and `onDone` once all have.
- `ui.go` — `appState` holds all app state (devices, settings, running/isPinging flags, sort
  column/direction, the ticker's stop channel) and builds the window content: a single-row
  toolbar (logo, add-device controls, Start/Clear pinned right via a spacer), the always-visible
  settings row, then the sortable header and table. Rows are hand-built widgets (a
  `canvas.Rectangle` background + a `container.NewGridWithColumns` grid, stacked), not a
  `widget.Table` — this was a deliberate choice: `widget.Table` recycles cell widgets via
  `CreateCell`/`UpdateCell`, which is a bad fit for a per-row remove button whose callback must
  stay bound to the right row index. `refreshRows` fully rebuilds all rows (and rebinds each
  remove button's closure to its current index) after structural changes (add/remove/clear/stop/
  sort); `updateRowResult` is the cheap per-cycle path that only touches one row's counters/color.
  Column headers (`newHeaderButton`) are plain `widget.Button`s that call `sortBy`, which toggles
  ascending/descending on repeat clicks of the same column and re-sorts `pm.devices` in place with
  `sort.SliceStable` (IP sorts numerically via `ipLess`/`net.ParseIP`, not lexicographically).
  Success/Fail/Total are raw counts; the derived **Loss** column (`formatLoss`) shows
  `fail/total` as a percentage — one decimal, trailing `.0` trimmed, so a single failure in a long
  run doesn't round to `%0`, and `-` before a device's first ping rather than a misleading `%0`.
  Sorting Loss uses the ratio (`lossRatio`), not the fail count, with never-pinged devices keyed
  to `-1` so they group together instead of tying with genuinely 0%-loss devices.
- `settings.go` — an always-visible settings row under the toolbar (no button to show/hide it):
  validated `widget.Entry` fields for interval/timeout apply on every valid `OnChanged`; interface
  is a `widget.Select` populated once from `net.Interfaces()` rather than free text. Changing the
  interval or interface while running calls `startTicker()` again so it takes effect on the next
  cycle instead of requiring a manual Stop/Start.
- `resizer.go` — small custom widgets/layouts for the table: `themedRect` (a rectangle whose fill
  is resolved from the live theme *at render time* — `buildUI` runs before the theme variant has
  settled, so `canvas.NewRectangle(theme.Color(...))` there silently picks dark-variant colors and
  renders near-black in a light window; this bit both the column dividers and the header band),
  `colResizer` (drag handle: `resizerWidth` grab area, `dividerThickness` visible line), and
  `singleColLayout`, which reads its width from a closure over `appState.colWidths` so a drag only
  needs a `Refresh()` rather than rebuilding widgets.
- `theme.go` — `appTheme` wraps `theme.DefaultTheme()` and overrides just `ColorNameSuccess`
  (a darker green than Fyne's default) and `ColorNameWarning` (a true yellow instead of Fyne's
  default orange), applied once via `a.Settings().SetTheme(...)` in `main.go`. Because
  `theme.SuccessColor()`/`ErrorColor()`/`widget.Importance` all resolve through the app's current
  theme, this one override is what keeps the Start button, Clear button, and the row
  success/fail tint (`tinted()` in ui.go) all pulling from the same palette.
- `main.go` — embeds `assets/*.png` via `//go:embed`, sets app metadata and the custom theme,
  builds the window.
- `packaging/` — `pms.desktop` + `build-deb.sh`, which hand-rolls a `DEBIAN/control` +
  `usr/bin`/`usr/share/...` tree and calls `dpkg-deb --build` (no extra packaging tool required;
  `fyne package` only produces a `.tar.gz` on Linux, not a `.deb`).

Key invariants:
- **Threading**: this app uses Fyne's `fyne.Do`/`fyne.DoAndWait` model (opted in via
  `app.SetMetadata(... Migrations: map[string]bool{"fyneDo": true})` in `main.go`). Any code that
  touches `appState` fields or Fyne widgets from a goroutine you spawn must go through
  `fyne.Do(...)`. Concretely: the ticker goroutine only calls `fyne.Do(func(){ pm.tick() })`, so
  `tick()` itself (which reads/writes `appState` fields) always runs on the main goroutine; the
  actual ping fan-out (`runCycle`, including its blocking semaphore) is launched via `go
  runCycle(...)` from inside `tick()` so it runs on a background goroutine and never blocks the
  UI, and its `onResult`/`onDone` callbacks wrap their bodies in `fyne.Do` to marshal back safely.
- A ping cycle is skipped (not queued) if the previous one hasn't finished (`isPinging` guard),
  matching the original Python behavior.
- Row background only reflects last-ping status while `running` is true; otherwise it's
  `color.Transparent` (not a hardcoded white), so it looks correct in both light and dark themes.
