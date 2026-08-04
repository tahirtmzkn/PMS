**PingInfoManager**
-----------------------
A small Fyne (Go) desktop app that continuously pings a list of IP addresses/devices and shows
live success/fail/total/loss counters, color-coded green/red by the last ping's result. It ships
as a single binary and installs from a `.deb` like any other desktop app.

> Renamed from **PMS** / *Ping Monitoring System*: `pms` is already taken by an unrelated package
> in the Ubuntu archive, so the binary, the Debian package and the config directory are all
> `pinginfomanager` now. A device list saved by the old version is carried over automatically the
> first time this one starts — see [Saved configuration](#saved-configuration).


Features
-----------------------
- Keeps an eye on a list of devices: one ping each per interval, with live Total, Success, Fail
  and Loss counters, and each row green while its last ping answered and red while it didn't.
- The interval is the send rate and nothing throttles it — 10 seconds at a 1-second interval is 10
  requests per device, reachable or not. The timeout only decides how long a reply stays valid.
- The whole list is pinged at once, so a round takes about as long as the timeout no matter how
  many devices are on it.
- Two name columns: **Name** is your own label for the device (optional — blank rows read
  `Unknown`), while **Hostname** is filled in automatically from the device's own SNMP hostname —
  when the device is added, and again every time you press **Start**.
- A status line summarising how many devices are up, down, or not yet pinged.
- Sortable and resizable columns, and rows you can drag into the order you want.
- Interval, timeout and network interface are set in the window and apply immediately.
- Light or dark, your choice: **Theme** follows the desktop by default and can be pinned to
  either, switching the whole window on the spot.
- The device list and the theme choice are saved automatically, so closing the app and reopening
  it gets the same devices back, in the same order, and the same look.


Requirements
-----------------------
Runtime, on Ubuntu/Debian:

| package | why |
| --- | --- |
| `iputils-ping` | the `ping` binary the app shells out to (installed by default) |
| `snmp` | the `snmpget` binary used for hostname resolution |

`snmp` is only needed for the Hostname column — without it that column just reads `Empty` and
everything else works. The MIB files (`snmp-mibs-downloader`) are **not** required: the app
queries the numeric OID precisely so it doesn't depend on them. The `.deb` declares both
packages, so `apt` pulls them in for you.

Ubuntu **18.04 or later**. The `.deb` states the glibc version it was built against, so apt
refuses to install it on anything older instead of installing a binary that cannot start.

The packaged binary uses X11, so on a Wayland session it runs through XWayland (present on every
mainstream Wayland desktop). That is what buys the 18.04 floor: glfw's Wayland backend needs
libwayland 1.20, and 18.04 has 1.16. A plain `go build` from source is unaffected and still builds
both backends.

To build from source you need Go 1.22+ and, once, the cgo/OpenGL/X11 headers:
```
sudo apt install -y gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev
```
Building the release `.deb` also needs Docker; see below for why.


Build & run
-----------------------
```
$ go build -o build/pinginfomanager .
$ ./build/pinginfomanager
```


Install as a .deb (Ubuntu)
-----------------------
```
$ ./packaging/build-deb.sh 1.2.0
$ sudo apt install ./dist/pinginfomanager_1.2.0_amd64.deb
```

This installs the `pinginfomanager` binary, a desktop entry and an icon, so it launches from the
application menu or via `pinginfomanager` in a terminal on any Ubuntu machine — no Go toolchain
needed on the target. Uninstall with `sudo apt remove pinginfomanager`.

The version is just the argument to the script (`dist/pinginfomanager_<version>_<arch>.deb`); the
script clears `dist/` on each run, so only the package you just built is left there.

**The build runs in a container, on purpose.** By default the script compiles the binary inside
Ubuntu 18.04 (`packaging/Dockerfile.build`), because a cgo binary needs a glibc at least as new as
the one it was linked against. Built on a 24.04 desktop, the binary picks up glibc 2.38's C23
redirects (`__isoc23_sscanf`, `__isoc23_strtol`, `__isoc23_strtoul`), which do not exist in
22.04's glibc 2.35, never mind 18.04's 2.27 — `dpkg -i` then succeeds and the program dies at
startup with ``libc.so.6: version `GLIBC_2.38' not found``. Compiling against 2.27 is forward
compatible, so one package covers 18.04, 20.04, 22.04, 24.04 and later. The image gets Go from
the upstream tarball at your own version, so container and local builds use the same toolchain.

The container build also passes `-tags x11`, which is the other half of reaching 18.04: the
default build compiles glfw's X11 *and* Wayland backends and chooses at runtime, but the Wayland
one needs `WL_MARSHAL_FLAG_DESTROY` from libwayland 1.20 and 18.04 ships 1.16, so it does not
compile there at all. The X11-only binary drops the Wayland and xkbcommon libraries from the
package's dependencies entirely.

`--local` skips the container and uses the host toolchain — quicker, no Docker needed, but the
result only runs on a glibc as new as your machine's. Use it to check the packaging, not to
produce anything you hand to someone else:
```
$ ./packaging/build-deb.sh --local 1.2.0
```

If you still have the old package installed, remove it separately — it was called `pms`, and
because that name belongs to a different program upstream the new package deliberately does not
declare a conflict with it:
```
$ sudo apt remove pms
```


Using it
-----------------------
1. Pick the network interface the devices are reachable through, in the settings row. Every
   ping is sent from that interface (`ping -I`), so this is not cosmetic.
2. Type an IP address, optionally a name, and press **Add** (or just hit Enter in either
   field). The name is your own label and goes in the **Name** column; leave it blank and the row
   reads `Unknown` there. Either way the **Hostname** column shows `Resolving…` for a moment and
   then the device's own SNMP hostname, or `Empty` if it didn't answer.
3. **Start** begins pinging, **Stop** ends it, **Clear** zeroes every counter without
   touching the list. Start also re-asks every device for its hostname, so the **Hostname**
   column goes back to `Resolving…` for a moment and then shows what the devices say now — a
   Stop/Start is how you refresh those names after swapping or renaming a device.
4. Click headers to sort, drag a header divider to resize a column, drag a row's grip to move
   it, and use the bin button to remove a device.

Settings:

| field | default | accepted |
| --- | --- | --- |
| Interval (s) | 1 | 1 and up — one request per device, this often |
| Timeout (ms) | 1000 | 1–10000 — how long a reply stays valid |
| Interface | `enp3s0` | the machine's interfaces, listed from the OS |
| Theme | System | System, Light, Dark |

Out-of-range input is simply not applied, so a half-typed value never takes effect.

How tight a reply window to accept is your call, which is why the timeout goes down to 1 ms. Below
a couple of milliseconds you are no longer measuring the network, though: the budget is timed from
when the `ping` process starts, so it also covers that process getting as far as sending. Against a
LAN device answering in well under a millisecond, 2 ms answered 20 of 20 requests here while 1 ms
dropped one of them.

**Theme** takes effect immediately and is remembered. `System` follows the desktop's own light/dark
preference (and `FYNE_THEME=light|dark` if you set it), which is the default; `Light` and `Dark`
pin the window regardless of what the desktop is doing. Interval, timeout and interface are *not*
remembered — they start at the defaults above on every launch. The device list is; see below.


Saved configuration
-----------------------
The device list and the theme choice are written to `~/.config/pinginfomanager/config.json` and read back at
startup, so the app reopens with the same devices in the same order and the same look. It is saved
whenever any of that changes — adding, removing, sorting or dragging a row, or picking a theme —
not on exit, so a crash or a `kill` doesn't lose it either.

Only what you chose is stored, one entry per device:
```json
{
  "theme": "dark",
  "devices": [
    { "ip": "10.0.0.1", "name": "Alpha" },
    { "ip": "10.0.0.2", "name": "Unknown" }
  ]
}
```

`theme` is `light` or `dark`, and is left out entirely while the theme follows the desktop.

**Upgrading from PMS.** The old version kept this file at `~/.config/pms/config.json`. On its
first start the renamed app copies that file to the new path, so the device list and theme survive
the rename. It only does so when the new file does not exist yet, so a list you have since edited
— including one you deliberately emptied — is never overwritten by the old one. The old file is
left where it is rather than deleted.

Counters are not saved — they measure one run, so every launch starts at zero. Hostnames are not
saved either: each device is asked for its SNMP hostname again on startup, so the column shows
what the device says now rather than what it said last week. The file is plain JSON in the
standard config directory, so it can be edited by hand or copied to another machine; if it ever
turns out to be unreadable the app starts with an empty list and leaves the file alone (the next
change to the list replaces it).


How it works
-----------------------
**Pinging.** Every **Interval**, each device gets exactly one echo request
(`ping -n -I <interface> -c 1`). That cadence is fixed: nothing waits for the previous request to be
answered first, so 10 seconds at a 1-second interval is 10 requests per device, whether the device
answers instantly or not at all. **Timeout** is a separate thing — how long a reply stays valid.
A request answered inside it counts as Success, one that isn't counts as Fail.

Set a timeout longer than the interval and requests simply overlap, exactly as `ping` itself does:
at a 1-second interval and a 5-second timeout each device has five requests outstanding.

That is also why **Total** counts requests *sent* rather than results collected. Every device's
Total advances at the same instant, so the column stays level across the table; Success and Fail
follow as the replies do, which for an unreachable device means one timeout later. **Loss** is
measured over the requests that have an outcome, not over Total, so it doesn't dip and recover
every time a request goes out.

The whole list goes out concurrently rather than in waves, so a round takes about as long as the
timeout no matter how many devices are on it. Rows are bound to devices by identity, not by list
position, so sorting, dragging or removing a device while its request is in flight can never land
one device's counters on another's row.

**Hostnames.** Every added device — and every device on the list each time a run is started —
gets, in the background:
```
snmpget -v2c -c public -t 1 -r 1 -Oqv [--clientaddr=<source>] <ip> 1.3.6.1.2.1.1.5.0
```
which is `SNMPv2-MIB::sysName.0` written numerically — the symbolic name only works where the
MIB files are installed, and Debian/Ubuntu ship the `snmp` package with MIB loading switched
off. One lookup is bounded at about two seconds, and anything other than a clean answer (no
reply, no SNMP on the device, `snmpget` not installed, an agent error) leaves the Hostname column
reading `Empty`. The **Name** column is never touched by this — it stays whatever you typed, or
`Unknown` if you typed nothing. `--clientaddr` is only added when the selected interface has an address on the
target's own subnet: one interface routinely carries several subnets, and binding the query to
the wrong one gets it sent with a source address the device can't reply to. For off-subnet
targets the kernel's route lookup picks the source instead.

Pressing **Stop** does not fire any queries — stopping is the app going quiet. Because one lookup
can sit on an unresponsive host for a couple of seconds, a quick Stop/Start can leave an earlier
round of queries still out; those answers are discarded when they arrive, so the newest round is
always what the column shows.

SNMP version and community are constants at the top of `snmp.go` (`v2c`, `public`) — change
them there if your gear uses something else.


Development
-----------------------
```
$ go build -o build/pinginfomanager .   # build
$ go vet ./...                          # static check
$ go test -race ./...                   # headless smoke test
```

`smoke_test.go` builds the entire UI against Fyne's headless test app — no window opens — and
exercises adding/removing devices, sorting, the column-resize drag, the row-reorder drag and
its animation, loss formatting, the status line, SNMP hostname resolution (with the lookup
stubbed, so tests never fork `snmpget`), the light/dark theme choice and the saved-config round
trip including the migration from the old config directory (against a temp directory, never your
real config). It's the intended way to check UI changes,
especially to the custom widgets, without putting a window on the screen.
