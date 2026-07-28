**PMS**
-----------------------
Ping Monitoring System — a small Fyne (Go) desktop app that continuously pings a list of
IP addresses/devices and shows live success/fail/total counters, color-coded green/red by
the last ping's result.


Build & run
-----------------------
Requires Go 1.22+ and (once, for cgo/OpenGL/X11) the system build dependencies:
```
sudo apt install -y gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev
```

```
$ go build -o build/pms .
$ ./build/pms
```


Install as a .deb (Ubuntu)
-----------------------
```
$ ./packaging/build-deb.sh 0.1.0
$ sudo apt install ./dist/pms_0.1.0_amd64.deb
```

This installs the `pms` binary, a desktop entry, and an icon, so it launches from the
application menu or via `pms` in a terminal on any Ubuntu machine — no Go toolchain or venv
needed on the target machine. Uninstall with `sudo apt remove pms`.
