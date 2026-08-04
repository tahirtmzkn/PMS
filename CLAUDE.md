# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

PingInfoManager is a small Go desktop app, built with the Fyne GUI toolkit
(`fyne.io/fyne/v2`), that continuously pings a user-defined list of IP addresses/devices and
displays live success/fail/total counters in a table, color-coded green (last ping succeeded)
or red (last ping failed). It's a from-scratch rewrite of an earlier PyQt5/Python version
(see git history) — the goals were a nicer default UI and a single installable binary instead
of a Python venv.

Two names are distinct on purpose and should not be conflated: `PingInfoManager` (the display name,
`appDisplayName` in `main.go` — window title and desktop entry) and `pinginfomanager` (Go module,
Debian package, binary, and the `~/.config` directory — lowercase because Debian requires it).

A third, `pms`, survives in **one** place only: `legacyConfigDirName` in `config.go`, read once by
`migrateLegacyConfig`. During development the app was briefly called that, and it stored its device
list at `~/.config/pms/config.json`; the constant exists so a machine that ran that build keeps its
list instead of looking like a first run. That name is not part of the released app and must not
appear anywhere user-facing — not in `README.md`, not in the package metadata, not in release notes.
The name was dropped because `pms` is an existing, unrelated package in the Ubuntu archive, which is
also why the `.deb` declares no `Conflicts:`/`Replaces:` on it (see `packaging/`).

## Commands

```
go build -o build/pinginfomanager .    # build
./build/pinginfomanager                # run
go vet ./...                           # static check
go test -race ./...                    # headless smoke test
./packaging/build-deb.sh 1.2.0         # release .deb, built in an Ubuntu 18.04 container
./packaging/build-deb.sh --local 1.2.0 # same, host toolchain — do not ship this one
```

**Release `.deb`s must be built in the container** (the default). The supported floor is Ubuntu
18.04, and a cgo binary needs a glibc at least as new as the one it linked against — see the
`packaging/` entry below for the full story. The packaged build is also X11-only (`-tags x11`),
which is the other half of reaching 18.04; a development `go build` is not, so the two do not
compile the same glfw backends.

**Do not drive the running app with synthetic clicks/keystrokes to test it.** This repo lives on
a desktop that also has live root SSH sessions to network gear on screen; misdirected synthetic
input is dangerous, and the user has said explicitly that they do the interactive testing
themselves. Verify with `go build`, `go vet`, and `go test`, then describe what changed and hand
off.

`smoke_test.go` is the only test: it builds the whole UI against `test.NewApp()` (headless, no
window opens) and exercises add/remove/sort/resize-drag/formatting plus the config round trip
(pointed at `t.TempDir()`, never the real `~/.config/pinginfomanager`). `appState.probe` holds
`pingOne` so a test can pin an outcome without forking a `ping` or needing a network —
`TestTickSendCadence` and `TestApplyProbeResult` use it to cover the send cadence and the counters.

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
  **`Total` counts requests *sent*, not results collected** — one per device per interval, written
  in a single pass in `tick`, so every device's Total advances at the same instant however the
  devices differ in reachability. `Success`/`Fail` are the outcomes and necessarily land later and
  at different moments (a reply comes back in ~1ms; a failure is only known a whole timeout later).
  `Resolved()` is `Success + Fail`, and `Total - Resolved()` is what is in flight — which is why
  Loss divides by `Resolved()`, see `formatLoss`.
  `failHeldUntil` + `showsFail(now)` are the **visual** side of a failure, separate from the
  counters: a row reads as failed while `!LastResult` *or* a recent failure is still being held.
  The hold exists because the timing hides losses otherwise — a failure is only known one timeout
  after its request went out, while the next request goes out one interval after it and is answered
  in ~1ms, so at the default 1s/1000ms a lost packet turned the row red and green again inside a
  frame or two. Only presentation is held; `Fail`/`Loss` count the failure the moment it is known.
- `ping.go` — `pingOne` shells out to the system `ping` binary
  (`ping -n -I <interface> -c 1 -W <whole_sec> <ip>`). `probeDevices` fans a request out to every
  device concurrently and calls `onResult` with `(*Device, ok)` as each finishes. `onResult` hands
  back the `*Device`, never its list index — the UI is free to reorder its list (sort, drag) while a
  request is still in flight. It deliberately **does not touch the counters**: rounds overlap, so
  two goroutines could otherwise be incrementing one device's `Success` at once. Every counter is
  written on the UI goroutine instead (`tick` / `applyProbeResult`). Three details are load-bearing
  for round time, all measured against a list of unreachable hosts:
  - `maxConcurrentPings` is 256, a backstop against forking absurd numbers of processes rather
    than a throttle. At its old value of 10 (copied from the Python version's QThreadPool) a
    40-device round took four ~1s waves — 4.0s instead of 1.0s — and on screen that read as the
    devices being pinged one after another. The semaphore (`pingSem`) is **package-level**, not
    per-round: rounds overlap whenever the timeout outlasts the interval, so a per-round semaphore
    would have stopped bounding anything. Note it bounds *processes*, not sends — `Total` counts a
    request when `tick` issues it, so a list long enough to queue on the semaphore has its actual
    spawns lag its Totals.
  - the semaphore is acquired *inside* each goroutine, not in the launch loop, so process spawns
    happen in parallel instead of queueing behind each other.
  - **the timeout is enforced by `runBounded`, not by ping's `-W`**, and `-W` is a whole number of
    seconds *rounded up* (`pingWaitArg`) — a coarse backstop that can only fire after the real
    deadline. It used to be the other way round (fractional `-W`, a `timeout + 2s` context as
    backstop), which is correct on 20.04 and later but hangs *forever* on 18.04: that iputils
    (s20161105) reads `-W` with `strtoul`, so "0.3" arrives as 0 and a 0 linger time waits for a
    reply that never comes (measured: still running after 12s). Truncating to whole seconds with a
    1s floor instead — the pre-1.1 behaviour — is what made every timeout setting below 1000ms
    cost a full second per unanswered host.
  `runBounded` starts the process, then arms a `time.AfterFunc` that kills it. Timing the budget
  from *after* the fork/exec is the whole reason it isn't `exec.CommandContext`: a context deadline
  has to include process spawn, so it needs a margin, and any margin is wrong in one direction or
  the other — too tight and a busy machine's slow spawn kills a ping before the device ever got its
  full timeout (a device that is up reads as down), too loose and every unanswered host holds its
  goroutine open for the slack. One probe therefore costs ~`timeout` + ~1ms, verified identically on
  24.04 and in an 18.04 container (100ms → 101ms, 300ms → 301ms, 1500ms → 1501ms). Killing racing
  `Wait` is safe: `os.Process` marks itself done under a lock as it is reaped, so the loser returns
  `ErrProcessDone` rather than signalling a recycled pid. Because the budget starts at process
  start it also covers ping's own setup, which is what bounds a *very* low timeout in practice:
  against a LAN device answering in well under a millisecond, 2ms answered 20/20 while 1ms dropped
  one of 20.
- `ui.go` — `appState` holds all app state (devices, settings, the `running` flag, the probe
  generation, sort column/direction, the ticker's stop channel) and builds the window content: a
  single-row
  toolbar (logo, add-device controls, Start/Clear pinned right via a spacer), the always-visible
  settings row, then the sortable header and table. Rows are hand-built widgets (a
  `canvas.Rectangle` background + a `container.NewGridWithColumns` grid, stacked), not a
  `widget.Table` — this was a deliberate choice: `widget.Table` recycles cell widgets via
  `CreateCell`/`UpdateCell`, which is a bad fit for per-row controls (the remove button and the
  reorder grip) that have to stay bound to their own device. **Rows are keyed by `*Device`, not by
  index**: `deviceRow.device` plus `indexOf`/`rowFor` mean every row callback resolves its position
  at click/update time, so reordering or removing can't leave a control aimed at the wrong device.
  `refreshRows` fully rebuilds all rows after structural changes (add/remove/clear/stop/sort);
  `updateRowResult` is the cheap path that only touches one row's counters/color, called both when a
  request goes out (Total) and when its outcome lands (Success/Fail/Loss/color), and it goes through
  `setLabel`/`colorRow`'s equality guards — `Label.SetText` and `Rectangle.Refresh` both repaint
  unconditionally, and on a steady list most of a round's counters and every row color are unchanged.
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
  The seven columns are Name, Hostname, IP, Total, Success, Fail, Loss, indexed by position in
  `colWidths` — the `columnCell` index is both the display position and the width slot, so inserting
  or moving one means renumbering those calls in *both* the header row and `newRow` (and keeping the
  two lists in the same order as each other). Their default widths have to keep the row inside the
  1200px default window or the trailing grip/remove cells get pushed off-screen (the table only
  scrolls vertically). Total leads the three counts because it is the one that advances for every
  device at the same instant — see `device.go` — so reading down it is how you see the run is even.
  Total/Success/Fail are raw counts; the derived **Loss** column (`formatLoss`) shows
  `fail/resolved` as a percentage — one decimal, trailing `.0` trimmed, so a single failure in a long
  run doesn't round to `%0`, and `-` before anything has been measured rather than a misleading `%0`.
  The denominator is `Device.Resolved()`, **not `Total`**: Total counts requests as they go out, so
  dividing by it would drop the figure the instant each request was sent and lift it back when the
  outcome arrived — a device losing everything would read `%92.3` for most of each second and `%100`
  in between. Sorting Loss uses the ratio (`lossRatio`), not the fail count, with devices that have
  nothing resolved keyed to `-1` so they group together instead of tying with genuinely 0%-loss ones.
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
  `migrateLegacyConfig` carries a device list forward from the directory a pre-release build used
  (`os.UserConfigDir()/pms/config.json`, `legacyConfigDirName`, reached via `legacyConfigPath()`),
  which the naming change moved; without it a machine that ran that build would read as a first run
  and silently drop the user's list. It is keyed on the **new file not
  existing**, never on its contents being empty — a user who removes every device leaves a valid
  config holding an empty list, and treating that as "nothing saved yet" would resurrect the old
  list on the next launch. It copies through `loadConfig`/`saveConfig` rather than byte for byte,
  so a corrupt old file is reported once here instead of being carried forward, and it leaves the
  old file on disk rather than deleting it. `main.go` runs it once, immediately after setting `configFile` and *before*
  `loadSavedConfig`, and only logs a failure — a config that can't be carried over is a first
  run, not a reason to refuse to start.
- `settings.go` — an always-visible settings row under the toolbar (no button to show/hide it):
  validated `widget.Entry` fields for interval/timeout apply on every valid `OnChanged`; interface
  is a `widget.Select` populated once from `net.Interfaces()` rather than free text. Changing the
  interval or interface while running calls `startTicker()` again so it takes effect on the next
  round instead of requiring a manual Stop/Start. Timeout accepts **1–10000 ms** — the floor is not
  a defensible network timeout but the operator's call, and the comment there records where the
  mechanism itself gives out (2ms answered 20/20 against a LAN device, 1ms dropped one of 20, since
  the budget starts at process start and so covers ping's own setup). The Theme `Select` (System/Light/Dark) calls
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
  Four things here are load-bearing, the first two learned from the 1.0.1 package failing on
  Ubuntu 22.04 and the third from taking the floor down to 18.04:
  - **The build runs in an Ubuntu 18.04 container by default.** 18.04 is the supported floor, and
    a cgo binary needs a glibc at least as new as the one it linked against. Compiled on this
    24.04 desktop the binary picks up glibc 2.38's C23 redirects — `__isoc23_sscanf`,
    `__isoc23_strtol`, `__isoc23_strtoul` — which do not exist in 22.04's glibc 2.35 (never mind
    18.04's 2.27), so `dpkg -i` succeeded and the program then died with
    ``libc.so.6: version `GLIBC_2.38' not found``. Linking against 2.27 is forward compatible, so
    one `.deb` covers 18.04 and everything later.
    `--local` builds with the host toolchain for checking the packaging, and must not be shipped.
    Go comes into the image from the upstream tarball at the host's own version (`GO_VERSION`
    build-arg), not from apt — 18.04 ships Go 1.10 — and not bind-mounted from the host either,
    since Ubuntu's golang packages symlink `src/`, `api/`, `misc/` and `test/` out to
    `/usr/share/go-<ver>/`, so mounting `/usr/lib/go-<ver>` alone arrives with a dangling `src/`
    and every build fails with "package unicode is not in std". The toolchain tarball is a static
    Go binary, so it runs in 18.04's userspace regardless. The module cache is mounted read-only
    with `GOPROXY=off` and `--user`, so the container needs no network and leaves nothing
    root-owned behind; `go mod download` runs on the host first, where the cache is writable.
    Bionic is still served by `archive.ubuntu.com` despite being out of standard support, so the
    image needs no `old-releases.ubuntu.com` rewriting — if that changes, `sources.list` has to be
    pointed there.
  - **The packaged build is X11-only (`-tags x11`), which is what makes 18.04 reachable at all.**
    The default build compiles glfw's X11 *and* Wayland backends and picks between them at runtime;
    the Wayland one needs `WL_MARSHAL_FLAG_DESTROY`, added in libwayland 1.20 (2021), and 18.04 has
    1.16 — the generated protocol headers do not compile there. Nor could such a binary be shipped
    to 18.04 even if it did: `wayland-client` is a hard `NEEDED` entry in that build, and the symbol
    is missing from 18.04's copy. With the tag, fyne's `wayland_csd_linux.go` (which is where the
    `#cgo pkg-config: wayland-client` comes from) drops out for `wayland_csd_other.go`'s
    `forcePlatform() == platformAuto`, and glfw picks the only platform it has. On a Wayland session
    the app then runs through XWayland, so the visible cost is confined to that case: no native
    Wayland surface, and `FYNE_PLATFORM=wayland` has nothing to select. A development `go build` is
    deliberately left alone, so the dual-backend build is still what gets exercised day to day.
    Also note EGL headers are needed even for the X11-only backend (glfw compiles `egl_context.c`
    unconditionally) and on 18.04 they are a separate `libegl1-mesa-dev` rather than something
    `libgl1-mesa-dev` pulls in.
  - **`Depends:` states the glibc floor, derived from the built binary** rather than hardcoded —
    `GLIBC_MIN` is the highest `GLIBC_x.y` symbol version `objdump -T` reports, emitted as
    `libc6 (>= x.y)`. This is what makes the failure mode impossible to reintroduce: a `--local`
    build on a newer machine now produces a package apt *refuses* to install on an older one,
    instead of one that installs and cannot start.
  - **The dependency list covers dlopened libraries too.** `libGL.so.1` is a hard ELF `NEEDED`
    entry, while glfw opens the X11 libraries with `dlopen` at runtime, so `dpkg` cannot see them
    and they have to be listed by hand — `strings` on the binary lists the candidates, `objdump -p`
    the hard ones. 1.0.1 listed neither `libxext6` nor `libxrender1`, and got away with it only
    because a desktop Ubuntu already has them. The Wayland and `libxkbcommon0` entries that used to
    be here went with the `-tags x11` switch: that binary references neither (checked both ways),
    and depending on packages 18.04 lacks or ships too old would have blocked the install for
    nothing.
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

  There is deliberately **no `Conflicts:`/`Replaces:` on `pms`**: that name belongs to an unrelated
  program in the Ubuntu archive, so declaring a conflict would fight that package rather than
  anything of ours. Nothing user-facing mentions the name at all — see the note at the top.

Key invariants:
- **Threading**: this app uses Fyne's `fyne.Do`/`fyne.DoAndWait` model (opted in via
  `app.SetMetadata(... Migrations: map[string]bool{"fyneDo": true})` in `main.go`). Any code that
  touches `appState` fields or Fyne widgets from a goroutine you spawn must go through
  `fyne.Do(...)`. Concretely: the ticker goroutine only calls `fyne.Do(func(){ pm.tick() })`, so
  `tick()` itself (which reads/writes `appState` fields, including every device's `Total`) always
  runs on the main goroutine; the actual ping fan-out (`probeDevices`, including its blocking
  semaphore) is launched via `go probeDevices(...)` from inside `tick()` so it runs on background
  goroutines and never blocks the UI, and its `onResult` callback wraps `applyProbeResult` in
  `fyne.Do` to marshal back safely. **`tick` and `applyProbeResult` are the only writers of a
  device's counters**, and since both run on the UI goroutine they cannot race — which is what makes
  overlapping rounds reporting on the same device safe.
- **The interval is the send cadence and nothing throttles it.** Every interval each device gets
  exactly one echo request, whether or not the previous one has been answered; the timeout only
  decides how long a reply stays valid. Rounds therefore overlap whenever the timeout outlasts the
  interval (interval 1s + timeout 5000ms means five requests outstanding per device — verified: 10
  requests sent in 10.4s, 5 resolved). This replaced an `isPinging` guard inherited from the Python
  version, which skipped a tick while a round was still in flight and so let the timeout silently
  change the rate: at the defaults (1s interval, 1000ms timeout) one unreachable device stretched the
  round past the interval and every second tick was dropped, so *every* device in the list — the
  reachable ones included — was probed once per two seconds (measured: 5 requests in 10.4s instead of
  10). `TestTickSendCadence` pins this.
- Row background only reflects last-ping status while `running` is true; otherwise it's
  `color.Transparent` (not a hardcoded white), so it looks correct in both light and dark themes.
- **A failure holds the row red for at least half the interval** (`failHoldDuration`, floored at
  `minFailHold`), so a single lost packet is something the eye can catch — see `device.go` for why
  it is otherwise a one-frame flash. `colorRow` and `refreshStatus` both read `Device.showsFail`,
  so the up/down tally can never say "0 down" beside a red row. The hold is a **floor, not a
  scheduled duration**: nothing arms a timer for its expiry, the row is simply repainted by
  whatever touches it next, which while running is at most one interval away (every `tick`
  recolors every row for its `Total`, and each device's own results land in between). That is what
  keeps it free of extra goroutines and of anything to cancel on Stop/Clear/remove — and a row
  cannot get stuck red, since `stop()` repaints all of them transparent. `TestFailHold` and
  `TestFailHoldDuration` pin it.
