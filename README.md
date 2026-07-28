**PMS**
-----------------------
Ping Monitoring System — a small Fyne (Go) desktop app that continuously pings a list of
IP addresses/devices and shows live success/fail/total/loss counters, color-coded green/red by
the last ping's result. It ships as a single binary and installs from a `.deb` like any other
desktop app.


Features
-----------------------
- Keeps an eye on a list of devices: one ping each per cycle, with live Success, Fail, Total
  and Loss counters, and each row green while its last ping answered and red while it didn't.
- The whole list is pinged at once, so a cycle takes about as long as the timeout no matter how
  many devices are on it.
- Two name columns: **Name** is your own label for the device (optional — blank rows read
  `Unknown`), while **Hostname** is filled in automatically from the device's own SNMP hostname.
- A status line summarising how many devices are up, down, or not yet pinged.
- Sortable and resizable columns, and rows you can drag into the order you want.
- Interval, timeout and network interface are set in the window and apply immediately.


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

To build from source you need Go 1.22+ and, once, the cgo/OpenGL/X11 headers:
```
sudo apt install -y gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev
```


Build & run
-----------------------
```
$ go build -o build/pms .
$ ./build/pms
```


Install as a .deb (Ubuntu)
-----------------------
```
$ ./packaging/build-deb.sh 0.8.0
$ sudo apt install ./dist/pms_0.8.0_amd64.deb
```

This installs the `pms` binary, a desktop entry and an icon, so it launches from the
application menu or via `pms` in a terminal on any Ubuntu machine — no Go toolchain needed on
the target. Uninstall with `sudo apt remove pms`.

The version is just the argument to the script (`dist/pms_<version>_<arch>.deb`); the script
clears `dist/` on each run, so only the package you just built is left there.


Using it
-----------------------
1. Pick the network interface the devices are reachable through, in the settings row. Every
   ping is sent from that interface (`ping -I`), so this is not cosmetic.
2. Type an IP address, optionally a name, and press **Add** (or just hit Enter in either
   field). The name is your own label and goes in the **Name** column; leave it blank and the row
   reads `Unknown` there. Either way the **Hostname** column shows `Resolving…` for a moment and
   then the device's own SNMP hostname, or `Empty` if it didn't answer.
3. **Start** begins the cycles, **Stop** ends them, **Clear** zeroes every counter without
   touching the list.
4. Click headers to sort, drag a header divider to resize a column, drag a row's grip to move
   it, and use the bin button to remove a device.

Settings:

| field | default | accepted |
| --- | --- | --- |
| Interval (s) | 1 | 1 and up — how long between cycles |
| Timeout (ms) | 1000 | 100–10000 — how long one ping waits for a reply |
| Interface | `enp3s0` | the machine's interfaces, listed from the OS |

Out-of-range input is simply not applied, so a half-typed value never takes effect.

The device list and the settings live for the session only — nothing is written to disk, so
both start fresh on the next launch.


How it works
-----------------------
**Pinging.** Each device gets one `ping -n -I <interface> -c 1 -W <timeout>` per cycle. The
whole list goes out concurrently rather than in waves, so cycle time tracks the timeout, not
the device count. A cycle is *skipped* rather than queued if the previous one hasn't finished
yet, so a list full of unreachable hosts can't build up a backlog. Rows are bound to devices
by identity, not by list position, so sorting, dragging or removing a device mid-cycle can
never land one device's counters on another's row.

**Hostnames.** Every added device gets, in the background:
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

SNMP version and community are constants at the top of `snmp.go` (`v2c`, `public`) — change
them there if your gear uses something else.


Development
-----------------------
```
$ go build -o build/pms .        # build
$ go vet ./...                   # static check
$ go test ./...                  # headless smoke test
```

`smoke_test.go` builds the entire UI against Fyne's headless test app — no window opens — and
exercises adding/removing devices, sorting, the column-resize drag, the row-reorder drag and
its animation, loss formatting, the status line and SNMP hostname resolution (with the lookup
stubbed, so tests never fork `snmpget`). It's the intended way to check UI changes, especially
to the custom widgets, without putting a window on the screen.
