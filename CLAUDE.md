# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

PingInfoManager is a small Go desktop app, built with the Fyne GUI toolkit
(`fyne.io/fyne/v2`), that continuously pings a user-defined list of IP addresses/devices and
displays live success/fail/total counters in a table, color-coded green (last ping succeeded)
or red (last ping failed). It's a from-scratch rewrite of an earlier PyQt5/Python version
(see git history) — the goals were a nicer default UI and a single installable binary instead
of a Python venv.

The app was called **PMS** (Ping Monitoring System) through 1.0.1. It was renamed because `pms`
is an existing, unrelated package in the Ubuntu archive, so every installable and on-disk name
collided with it. Three names are now distinct on purpose and should not be conflated:
`PingInfoManager` (the display name, `appDisplayName` in `main.go` — window title and desktop
entry), `pinginfomanager` (Go module, Debian package, binary, and the `~/.config` directory —
lowercase because Debian requires it), and `pms`, which now appears **only** in the two places
that must still recognise the old world: `legacyConfigDirName` in `config.go` and the upgrade
notes in `README.md`.

## Commands

```
go build -o build/pinginfomanager .    # build
./build/pinginfomanager                # run
go vet ./...                           # static check
go test -race ./...                    # headless smoke test
./packaging/build-deb.sh 1.1.0         # release .deb, built in an Ubuntu 22.04 container
./packaging/build-deb.sh --local 1.1.0 # same, host toolchain — do not ship this one
```

**Release `.deb`s must be built in the container** (the default). The supported floor is Ubuntu
22.04, and a cgo binary needs a glibc at least as new as the one it linked against — see the
`packaging/` entry below for the full story.

**Do not drive the running app with synthetic clicks/keystrokes to test it.** This repo lives on
a desktop that also has live root SSH sessions to network gear on screen; misdirected synthetic
input is dangerous, and the user has said explicitly that they do the interactive testing
themselves. Verify with `go build`, `go vet`, and `go test`, then describe what changed and hand
off.

`smoke_test.go` is the only test: it builds the whole UI against `test.NewApp()` (headless, no
window opens) and exercises add/remove/sort/resize-drag/formatting plus the config round trip
(pointed at `t.TempDir()`, never the real `~/.config/pinginfomanager`).

It is expected to stay clean under `go test -race ./...`, which constrains how tests drive
hostname lookups. `test.NewApp`'s driver runs `fyne.Do` *inline on the calling goroutine*
(`test/driver.go`) instead of marshalling it to a UI thread, so any code path that starts several
lookups at once (`restoreDevices`, `refreshHostnames`) would, in a test, have several goroutines
inside Fyne's widget code simultaneously — a race in Fyne's own font cache that the real app
cannot hit, since its driver funnels every `fyne.Do` onto one goroutine. Tests therefore let one
answer land at a time: either add devices one at a time and wait on `addDevice`'s channel, or use
the `serialRestore` helper, which parks each stubbed lookup on a per-IP gate and releases them in
list order. It exists so UI changes —
especially custom widgets like `colResizer` — can be checked for panics and logic regressions
without opening a window on the user's screen. Extend it rather than launching the app.

Building requires cgo + OpenGL/X11 dev headers (one-time, Ubuntu 24.04):
```
sudo apt install -y gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev
```

## Architecture

- `device.go` — `Device` struct: one monitored target's IP, user-typed `Name` and SNMP-resolved
  `Hostname`, plus running success/fail/total counters and `LastResult`. `Name` and `Hostname` are
  separate on purpose: the name box is optional and its text is the only thing the Name column
  ever shows, while `Hostname` is always filled in from the device itself.
- `ping.go` — `pingOne` shells out to the system `ping` binary
  (`ping -n -I <interface> -c 1 -W <fractional_sec> <ip>`). `runCycle` fans a ping out to every
  device concurrently, calling `onResult` as each device finishes and `onDone` once all have.
  `onResult` hands back the `*Device`, never its list index — the UI is free to reorder its list
  (sort, drag) while a cycle is still in flight. Three details are load-bearing for cycle time,
  all measured against a list of unreachable hosts:
  - `maxConcurrentPings` is 256, a backstop against forking absurd numbers of processes rather
    than a throttle. At its old value of 10 (copied from the Python version's QThreadPool) a
    40-device cycle took four ~1s waves — 4.0s instead of 1.0s — and on screen that read as the
    devices being pinged one after another.
  - the semaphore is acquired *inside* each goroutine, not in the launch loop, so process spawns
    happen in parallel instead of queueing behind each other.
  - `-W` is passed as fractional seconds (`pingWaitArg`). Truncating to whole seconds with a 1s
    floor made every timeout setting below 1000ms cost a full second per unanswered host: a
    40-device list at a 300ms timeout took 4.0s, now 0.3s.
  Cycle wall time is therefore ~`timeout` + ~10ms regardless of device count (verified to 100).
- `ui.go` — `appState` holds all app state (devices, settings, running/isPinging flags, sort
  column/direction, the ticker's stop channel) and builds the window content: a single-row
  toolbar (logo, add-device controls, Start/Clear pinned right via a spacer), the always-visible
  settings row, then the sortable header and table. Rows are hand-built widgets (a
  `canvas.Rectangle` background + a `container.NewGridWithColumns` grid, stacked), not a
  `widget.Table` — this was a deliberate choice: `widget.Table` recycles cell widgets via
  `CreateCell`/`UpdateCell`, which is a bad fit for per-row controls (the remove button and the
  reorder grip) that have to stay bound to their own device. **Rows are keyed by `*Device`, not by
  index**: `deviceRow.device` plus `indexOf`/`rowFor` mean every row callback resolves its position
  at click/update time, so reordering or removing can't leave a control aimed at the wrong device.
  `refreshRows` fully rebuilds all rows after structural changes (add/remove/clear/stop/sort);
  `updateRowResult` is the cheap per-cycle path that only touches one row's counters/color, and
  it goes through `setLabel`/`colorRow`'s equality guards — `Label.SetText` and
  `Rectangle.Refresh` both repaint unconditionally, and on a steady list most of a cycle's
  counters and every row color are unchanged.
  Each row ends with a reorder grip (`dragHandle`, see `resizer.go`) and the remove button.
  Dragging the grip runs `dragRow`, which accumulates vertical travel in `dragOffset`: the row is
  drawn at that offset so it follows the pointer, and once the travel covers one `rowStride()`
  (row height + `theme.Padding()`) `swapRows` exchanges it with its neighbour while the offset
  drops by the same stride — so the row's screen position is continuous across a swap. `swapRows`
  deliberately does *not* call `refreshRows` — a rebuild mid-drag would destroy the very grip
  widget the driver is delivering `Dragged` events to — it swaps `devices`/`rows` together and
  clears `sortCol` (a hand-ordered list makes any header arrow a lie).
  Drag animation is three cooperating pieces:
    - `rowOffsets` (keyed by row content object, read by `rowsLayout`) is how far each row is drawn
      from its slot; `rowAnims` holds the in-flight animations so a new drag can cut one short.
      `refreshRows` stops and clears both, since the rows they move are being discarded.
    - `animateRowOffset` eases an offset back to 0 over `canvas.DurationShort` with
      `AnimationEaseOut` — used for the row displaced by a swap (started at ±one stride so it
      slides into the vacated slot) and for the dragged row settling on release. Animation `Tick`
      callbacks run on the main draw loop (`TickAnimations` is called from the driver's paint
      loop), so they must **not** be wrapped in `fyne.Do` — that logs a "called from main
      goroutine" error. Ticks call `layoutRows` (re-layout + `canvas.Refresh`) rather than
      `Container.Refresh`, which would deep-refresh every label each frame.
      `rowSlideAnimation` returns the unstarted animation so tests can step it frame by frame —
      Fyne's *test* driver finishes any animation it is handed instantly on `Start`.
    - a dragged row is `lifted`: `syncPaintOrder` moves it to the end of `rowsContainer.Objects`
      so it paints above the rows it passes, and `colorRow` composites its tint onto the window
      background (`opaqueOver`) plus a hairline outline. Both are needed — a translucent row would
      let the row underneath show through it. It stays lifted until the settle animation lands.
  Column headers (`newHeaderButton`) are plain `widget.Button`s that call `sortBy`, which toggles
  ascending/descending on repeat clicks of the same column and re-sorts `pm.devices` in place with
  `sort.SliceStable` (IP sorts numerically via `ipLess`/`net.ParseIP`, not lexicographically).
  The seven columns are Name, Hostname, IP, Success, Fail, Total, Loss, indexed by position in
  `colWidths` — inserting or moving one means renumbering the `columnCell` calls in *both* the
  header row and `newRow`, and their default widths have to keep the row inside the 1200px default
  window or the trailing grip/remove cells get pushed off-screen (the table only scrolls vertically).
  Success/Fail/Total are raw counts; the derived **Loss** column (`formatLoss`) shows
  `fail/total` as a percentage — one decimal, trailing `.0` trimmed, so a single failure in a long
  run doesn't round to `%0`, and `-` before a device's first ping rather than a misleading `%0`.
  Sorting Loss uses the ratio (`lossRatio`), not the fail count, with never-pinged devices keyed
  to `-1` so they group together instead of tying with genuinely 0%-loss devices.
  The **Name** column is only ever the text from the (optional) name box, or `unknownName`
  ("Unknown") when it was left blank — it is never written from SNMP. The separate **Hostname**
  column is what the device calls itself: every added device goes up with
  `resolvingHostname` there while `startHostnameLookup` asks it for its SNMP sysName on a background
  goroutine, and `applyResolvedHostname` (via `fyne.Do`) writes the answer — or `emptyHostname`
  ("Empty") if nothing replied — onto the device and its row label. The lookup runs whether or not
  a name was typed, since the two columns are independent now. The device is carried by pointer, so
  a sort/drag/remove mid-query is harmless (`indexOf` < 0 means the answer is dropped). `addDevice`
  returns the lookup's completion channel purely so tests can wait for it; the UI ignores it.
  So "Unknown" in Name means *you didn't name it*, while "Empty" in Hostname means *it didn't
  answer* — two different facts that the old single-column behaviour conflated.
  `start()` re-asks *every* device via `refreshHostnames` (each column back to `resolvingHostname`
  first), so a run begins from what the devices say now rather than from an answer that may be
  hours old — a switch renamed, replaced or only now reachable shows up. `stop()` deliberately
  does not: stopping is the app going quiet. That makes overlapping lookups routine (a Stop/Start
  while a query is still parked on a dead host for its ~2s budget), so `appState.hostnameGen`
  counts refreshes, `startHostnameLookup` captures the generation it started in, and its `fyne.Do`
  callback drops the answer if a later refresh has since superseded it — otherwise the old query's
  "Empty" lands on top of the new query's name. `start()` returns the lookups' completion channels
  for the same reason `addDevice` does: tests wait, the UI ignores.
- `snmp.go` — `snmpSysName` shells out to `snmpget` for `sysName.0`. Details worth keeping:
  the OID is written numerically (`1.3.6.1.2.1.1.5.0`) because Debian/Ubuntu's `snmp` package
  disables MIB loading, where the symbolic `SNMPv2-MIB::sysName.0` is rejected outright; `-Oqv`
  prints the bare value, but `parseSysName` still handles the `OID = STRING: "name"` form and has
  to reject agent errors ("No Such Object…") itself since those arrive on stdout with exit status
  0; `-t`/`-r` bound one lookup to ~2s so a row doesn't sit on the placeholder; and the query is
  sourced from the selected interface (`--clientaddr`) *only* when `interfaceSourceIP` finds an
  address on that interface whose subnet contains the target. One interface routinely carries
  several subnets — this desktop's enp3s0 has four — so binding to its first address sent every
  query out with a source the device couldn't answer, turning every name into "Unknown" while the
  same query worked by hand. For an off-subnet target the flag is omitted and the kernel's route
  lookup picks the source, which it does correctly.
  The community/version are constants, not settings fields — a wrong one just yields "Unknown".
  `appState.lookupName` holds this function so tests can stub it instead of forking a subprocess.
- `config.go` — saves and restores the device list and the light/dark choice as JSON at
  `os.UserConfigDir()/pinginfomanager/config.json` (`configDirName`, via `configPathIn`).
  Deliberate choices: only `{ip, name}` per device is
  stored — counters measure one run, and `Hostname` is re-asked over SNMP on load (a remembered
  sysName would go stale silently, and `restoreDevices` starts the same lookup `addDevice` does,
  so a restored row shows `Resolving…` then the live answer or `Empty`); `theme` is `"light"`/
  `"dark"` and **absent** when following the desktop, so the default writes nothing;
  interval/timeout/interface stay session-only. Saving happens at the places the saved state
  changes (`addDevice`, `removeDevice`, `sortBy`, `endRowDrag`, and the theme Select) rather
  than in a window-close hook, so a crash or `kill` doesn't lose it — `endRowDrag` rather than
  `swapRows` so one drag across the table is one write; `saveConfig` writes a temp file in the
  same directory and renames it over the target, so an interrupted save can't replace a good
  list with a truncated one; a missing file is a first run (not an error) while a corrupt one is
  logged and left alone rather than overwritten. `appState.configFile` holds the path and is
  **empty by default** — `main.go` fills it in from `defaultConfigPath()`, which is what keeps
  every test that doesn't opt in from writing over the user's real device list.
  The file is read **once**, by `loadSavedConfig`, and applied in two parts because they need
  opposite sides of `buildUI`: the theme before it (first paint in the right palette) and
  `restoreDevices(saved.Devices)` after it (the lookups need rows, and the settings row's Select
  has to exist). `restoreDevices` takes the list rather than re-reading the file, and returns the
  lookups' completion channels for tests; its `fyne.Do` callbacks landing before `ShowAndRun` is
  safe, the glfw driver queues them on an unbounded channel until the loop starts.
  `migrateLegacyConfig` carries a device list across the rename, which moved this directory:
  the pre-1.1.0 app stored the same file at `os.UserConfigDir()/pms/config.json`
  (`legacyConfigDirName`, reached via `legacyConfigPath()`), and without the migration the rename
  would read as a first run and silently drop the user's list. It is keyed on the **new file not
  existing**, never on its contents being empty — a user who removes every device leaves a valid
  config holding an empty list, and treating that as "nothing saved yet" would resurrect the old
  list on the next launch. It copies through `loadConfig`/`saveConfig` rather than byte for byte,
  so a corrupt old file is reported once here instead of being carried forward, and it leaves the
  old file on disk rather than deleting it (a downgrade to the 1.0.1 package still finds its
  list). `main.go` runs it once, immediately after setting `configFile` and *before*
  `loadSavedConfig`, and only logs a failure — a config that can't be carried over is a first
  run, not a reason to refuse to start.
- `settings.go` — an always-visible settings row under the toolbar (no button to show/hide it):
  validated `widget.Entry` fields for interval/timeout apply on every valid `OnChanged`; interface
  is a `widget.Select` populated once from `net.Interfaces()` rather than free text. Changing the
  interval or interface while running calls `startTicker()` again so it takes effect on the next
  cycle instead of requiring a manual Stop/Start. The Theme `Select` (System/Light/Dark) calls
  `applyThemeMode` + `persistConfig`, and sets its initial value by **assigning `Selected`, not
  `SetSelected`** — this is load-bearing: `SetSelected` fires `OnChanged`, and `buildUI` runs before
  the saved device list has been restored, so the callback's `persistConfig` would write an empty
  list over the saved one. There is nothing for it to apply anyway, `main.go` having already
  applied the mode.
- `resizer.go` — small custom widgets/layouts for the table: `themedRect` (a rectangle whose fill
  is resolved from the live theme *at render time* — `buildUI` runs before the theme variant has
  settled, so `canvas.NewRectangle(theme.Color(...))` there silently picks dark-variant colors and
  renders near-black in a light window; this bit both the column dividers and the header band),
  `colResizer` (column divider: `resizerWidth` grab area, `dividerThickness` visible line),
  `dragHandle` (the row-reorder grip; it *extends `widget.Button`* with `Dragged`/`DragEnd` rather
  than being hand-rolled, which is what makes its footprint and glyph size identical to the remove
  button beside it — both draw at `theme.SizeNameInlineIcon` — and it deliberately has no
  `OnTapped`, so a click that never crosses the drag threshold does nothing), `singleColLayout`,
  which reads its width from a closure over `appState.colWidths` so a drag only needs a
  `Refresh()` rather than rebuilding widgets, and `rowsLayout`, the VBox replacement the rows sit
  in: it takes slot order from a closure over `appState.rows` instead of from the container's
  `Objects` order (which frees `Objects` to carry paint order, so the dragged row can be painted
  last) and adds each row's `rowOffsets` entry to its Y. Its `MinSize` ignores those offsets, so a
  row leaning out of its slot never resizes the scroll content.
- `theme.go` — `appTheme` wraps `theme.DefaultTheme()` and does two things. It overrides
  `ColorNameSuccess` (a darker green than Fyne's default) and `ColorNameWarning` (a true yellow
  instead of Fyne's default orange) — the same values in both variants on purpose, since green
  means "up" either way. Because `theme.SuccessColor()`/`ErrorColor()`/`widget.Importance` all
  resolve through the app's current theme, that one override is what keeps the Start button, Clear
  button and the row success/fail tint (`tinted()` in ui.go) pulling from the same palette.
  It also carries a `themeMode` (`themeSystem`/`themeLight`/`themeDark`) and **substitutes the
  variant** in `Color`. That substitution *is* dark mode: `theme.DefaultTheme()` picks its light or
  dark palette purely from the variant argument it is handed (`theme/theme.go`'s
  `builtinTheme.Color`), so replacing it flips every color in the app, and there is nothing else to
  override. `themeSystem` passes the variant through, which is what keeps the desktop preference —
  and `FYNE_THEME` / Fyne's own settings file — working as before.
  `appState.applyThemeMode` installs the choice with `Settings().SetTheme(newAppTheme(mode))`: a
  *fresh* theme value, because Fyne's settings change is what clears the theme caches and walks
  every window refreshing widgets (`internal/app.ApplyThemeTo`), which is how `themedRect`
  re-resolves. It then calls `refreshRows` on top, because a row's background is a *computed* color
  sitting in a `canvas.Rectangle` (the low-alpha success/error tint, and the window background
  composited under a lifted row) — Fyne re-resolves theme colors, not ours, so without the rebuild
  the rows keep the old palette's tint until some later cycle happens to change one.
  `main.go` applies the saved mode *before* `buildUI`, so the first paint is already in the right
  palette rather than flipping after the window appears.
- `main.go` — holds `appDisplayName`, embeds `assets/*.png` via `//go:embed`, sets app metadata,
  then runs one fixed order that the rest of the app depends on: point `configFile` at
  `defaultConfigPath()`, `migrateLegacyConfig` from `legacyConfigPath()`, read the file once
  (`loadSavedConfig`), `applyThemeMode` — which also installs the custom theme, saved choice or
  not — then `buildUI`/`SetContent`, then `restoreDevices`, then `ShowAndRun`.
- `packaging/` — `pinginfomanager.desktop` + `build-deb.sh`, which hand-rolls a `DEBIAN/control` +
  `usr/bin`/`usr/share/...` tree and calls `dpkg-deb --build` (no extra packaging tool required;
  `fyne package` only produces a `.tar.gz` on Linux, not a `.deb`), plus `Dockerfile.build`.
  Three things here are load-bearing, all learned from the 1.0.1 package failing on Ubuntu 22.04:
  - **The build runs in an Ubuntu 22.04 container by default.** 22.04 is the supported floor, and
    a cgo binary needs a glibc at least as new as the one it linked against. Compiled on this
    24.04 desktop the binary picks up glibc 2.38's C23 redirects — `__isoc23_sscanf`,
    `__isoc23_strtol`, `__isoc23_strtoul` — which do not exist in 22.04's glibc 2.35, so `dpkg -i`
    succeeded and the program then died with ``libc.so.6: version `GLIBC_2.38' not found``.
    Linking against 2.35 is forward compatible, so one `.deb` covers 22.04 and everything later.
    `--local` builds with the host toolchain for checking the packaging, and must not be shipped.
    The image installs no Go: the script bind-mounts the host's `GOROOT` (the Go toolchain is
    statically linked, so it runs in 22.04's older userspace) plus the module cache read-only,
    with `GOPROXY=off` and `--user` so the container needs no network and leaves nothing
    root-owned behind. `go mod download` runs on the host first, where the cache is writable.
  - **`Depends:` states the glibc floor, derived from the built binary** rather than hardcoded —
    `GLIBC_MIN` is the highest `GLIBC_x.y` symbol version `objdump -T` reports, emitted as
    `libc6 (>= x.y)`. This is what makes the failure mode impossible to reintroduce: a `--local`
    build on a newer machine now produces a package apt *refuses* to install on an older one,
    instead of one that installs and cannot start.
  - **The dependency list covers dlopened libraries too.** `libGL.so.1` and
    `libwayland-client.so.0` are hard ELF `NEEDED` entries, while glfw opens the X11, xkbcommon
    and remaining Wayland libraries with `dlopen` at runtime, picking a set according to the
    session — so both sets have to be installed. 1.0.1 listed neither `libwayland-client0` (a hard
    requirement it got away with only because a desktop Ubuntu already has it) nor `libxext6`,
    `libxrender1` or `libxkbcommon0`. `strings` on the binary lists the candidates.
  - **The packaged build alone gets `-trimpath` and `-ldflags="-s -w"`**; a development
    `go build` keeps everything. `-s -w` drops the symbol table and DWARF: measured at 1.0.1 the
    binary went 31.4MB -> 23.2MB and the `.deb` 15.7MB -> 9.6MB, and panic traces still name
    functions and lines because Go reads those from the pclntab, which `-s -w` leaves alone; what
    is lost is attaching gdb/delve to the installed binary. `-trimpath` rewrites the ~634 absolute
    source paths the compiler embeds into module-relative ones, so a trace reads
    `fyne.io/fyne/v2@v2.8.0/app/cache.go` and not a path on the build machine — which through
    1.0.1 was the maintainer's home directory, and once the build moved into the container became
    `/src` and `/gomodcache`. It also makes the container build byte-for-byte reproducible, so a
    release can be checked by rebuilding it (verified: two runs, identical binaries). That holds
    *within* one builder image only — a container build and a `--local` build of identical source
    still differ, since cgo compiles against whichever gcc and glibc the environment has (also
    verified). Neither flag costs anything for a package that is not debugged in place.

  There is deliberately **no `Conflicts:`/`Replaces:` on `pms`**: the old package shared that name
  with an unrelated program in the Ubuntu archive, so declaring a conflict would fight that
  package rather than our own history. `README.md` tells upgraders to `apt remove pms` themselves.

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
