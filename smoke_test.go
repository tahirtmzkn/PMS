package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestSmokeBuildUI(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	win := a.NewWindow("PMS")
	pm := newAppState(win, fyne.NewStaticResource("trash.png", trashPNG))
	content := pm.buildUI(fyne.NewStaticResource("ping-pong.png", pingPongPNG))
	win.SetContent(content)
	win.Resize(fyne.NewSize(1200, 700))

	// Every device gets an SNMP hostname lookup; stub it so no test forks
	// snmpget.
	pm.lookupName = func(ip, iface string) string { return "" }

	// Waiting on the lookup before touching the table again is a test-driver
	// detail: test.NewApp runs fyne.Do inline on the calling goroutine instead
	// of marshalling it to a UI thread, so an outstanding lookup would other-
	// wise land in the middle of the next refreshRows.
	<-pm.addDevice("10.0.0.2", "")
	<-pm.addDevice("9.0.0.1", "Beta")
	if len(pm.devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(pm.devices))
	}
	// A blank name is "Unknown" — the Name column is never filled in from SNMP.
	if pm.devices[0].Name != unknownName {
		t.Errorf("blank name = %q, want %q", pm.devices[0].Name, unknownName)
	}
	if pm.devices[0].Hostname != emptyHostname {
		t.Errorf("unresolvable hostname = %q, want %q", pm.devices[0].Hostname, emptyHostname)
	}

	// numeric IP ordering, not lexicographic
	pm.sortBy(sortIP)
	if pm.devices[0].IP != "9.0.0.1" {
		t.Errorf("ip sort asc first = %q, want 9.0.0.1", pm.devices[0].IP)
	}
	pm.sortBy(sortIP)
	if pm.devices[0].IP != "10.0.0.2" {
		t.Errorf("ip sort desc first = %q, want 10.0.0.2", pm.devices[0].IP)
	}

	if got := formatLoss(10, 20); got != "%50" {
		t.Errorf("formatLoss = %q", got)
	}
	if got := formatLoss(1, 1000); got != "%0.1" {
		t.Errorf("formatLoss fraction = %q", got)
	}
	if got := formatLoss(0, 0); got != "-" {
		t.Errorf("formatLoss unpinged = %q", got)
	}

	// Loss sorts on the fail/total ratio, not the raw fail count, and
	// never-pinged devices stay grouped ahead of measured ones.
	pm.devices[0].Fail, pm.devices[0].Total = 1, 10 // 10%
	pm.devices[1].Fail, pm.devices[1].Total = 5, 10 // 50%
	<-pm.addDevice("10.0.0.9", "Unpinged")
	pm.sortBy(sortLoss)
	if pm.devices[0].Name != "Unpinged" || pm.devices[2].Fail != 5 {
		t.Errorf("loss sort asc = %v", []string{pm.devices[0].Name, pm.devices[1].Name, pm.devices[2].Name})
	}
	pm.sortBy(sortLoss)
	if pm.devices[0].Fail != 5 || pm.devices[2].Name != "Unpinged" {
		t.Errorf("loss sort desc = %v", []string{pm.devices[0].Name, pm.devices[1].Name, pm.devices[2].Name})
	}
	pm.removeDevice(2)
	pm.clearStats()

	// exercise the resizer drag path (custom renderer + layout)
	r := newColResizer(func(dx float32) { pm.colWidths[0] += dx })
	_ = test.WidgetRenderer(r)
	r.Refresh()
	before := pm.colWidths[0]
	r.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(25, 0)})
	r.DragEnd()
	if pm.colWidths[0] != before+25 {
		t.Errorf("drag width = %v, want %v", pm.colWidths[0], before+25)
	}

	pm.removeDevice(0)
	if len(pm.devices) != 1 {
		t.Errorf("after remove, len = %d", len(pm.devices))
	}
	pm.clearStats()
}

// TestRowReorder covers the drag-to-reorder grip: that travel has to cover a
// whole row before anything moves, that pm.rows stays in step with pm.devices
// (otherwise a cycle's counters land on the wrong row), and that dragging past
// either end is a no-op rather than a panic.
func TestRowReorder(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	win := a.NewWindow("PMS")
	pm := newAppState(win, fyne.NewStaticResource("trash.png", trashPNG))
	win.SetContent(pm.buildUI(fyne.NewStaticResource("ping-pong.png", pingPongPNG)))
	win.Resize(fyne.NewSize(1200, 700))

	pm.lookupName = func(ip, iface string) string { return "" }
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		<-pm.addDevice(ip, ip)
	}
	pm.sortBy(sortIP) // an active sort, whose arrow a manual reorder must drop
	first := pm.devices[0]

	stride := pm.rowStride()
	if stride <= 0 {
		t.Fatalf("rowStride = %v", stride)
	}

	pm.dragRow(first, stride/2)
	if pm.devices[0] != first {
		t.Errorf("half a row of travel moved the device")
	}

	// ...but the row itself follows the pointer, lifted above its neighbours
	row := pm.rowFor(first)
	if !row.lifted {
		t.Errorf("dragged row was not lifted")
	}
	if got := pm.rowOffsets[row.content]; got != stride/2 {
		t.Errorf("row offset = %v, want %v", got, stride/2)
	}
	if objs := pm.rowsContainer.Objects; objs[len(objs)-1] != row.content {
		t.Errorf("lifted row is not painted last")
	}
	if _, _, _, alpha := row.bg.FillColor.RGBA(); alpha != 0xffff {
		t.Errorf("lifted row background is translucent, alpha = %d", alpha)
	}

	pm.dragRow(first, stride/2)
	if pm.devices[1] != first {
		t.Errorf("a full row of travel did not move the device down")
	}
	if pm.sortCol != sortNone {
		t.Errorf("sort indicator survived a manual reorder")
	}
	for i, d := range pm.devices {
		if pm.rows[i].device != d {
			t.Fatalf("row %d bound to %q, want %q", i, pm.rows[i].device.IP, d.IP)
		}
	}

	// overshooting the end clamps at the last row instead of panicking
	pm.dragRow(first, stride*5)
	if pm.devices[len(pm.devices)-1] != first {
		t.Errorf("device did not reach the bottom row")
	}

	// releasing part-way through a row settles it: no lift, no offset
	pm.dragRow(first, -stride/3)
	pm.endRowDrag()
	if row.lifted {
		t.Errorf("row still lifted after settling")
	}
	if _, ok := pm.rowOffsets[row.content]; ok {
		t.Errorf("settled row left an offset behind")
	}

	// the settle above is instant here (the test driver finishes animations on
	// Start), so drive the interpolation by hand to check it eases rather than
	// jumps, and cleans up its offset at the end
	pm.rowOffsets[row.content] = stride
	anim := pm.rowSlideAnimation(row, stride)
	anim.Tick(0.5)
	if off := pm.rowOffsets[row.content]; off <= 0 || off >= stride {
		t.Errorf("mid-slide offset = %v, want between 0 and %v", off, stride)
	}
	anim.Tick(1)
	if _, ok := pm.rowOffsets[row.content]; ok {
		t.Errorf("finished slide left an offset behind")
	}

	// counters must follow the device to wherever its row now is
	first.Success, first.Total = 2, 2
	pm.updateRowResult(first)
	if got := pm.rowFor(first).success.Text; got != "2" {
		t.Errorf("success text = %q, want 2", got)
	}

	// a device removed while its ping is still in flight simply has no row
	pm.removeDevice(pm.indexOf(first))
	if pm.rowFor(first) != nil {
		t.Errorf("removed device still has a row")
	}
	pm.updateRowResult(first)

	// the grip widget itself: renderer plus drag hooks
	moved, ended := float32(0), false
	h := newDragHandle(func(dy float32) { moved += dy }, func() { ended = true })
	_ = test.WidgetRenderer(h)
	h.Refresh()
	h.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(0, 12)})
	h.DragEnd()
	if moved != 12 || !ended {
		t.Errorf("handle drag = %v, ended = %v", moved, ended)
	}
}

// TestHostnameLookup covers the Hostname column: the placeholder while the SNMP
// query is in flight, the answer landing on both the device and its row, the
// "Empty" fallback when nothing answers, the Name column being left alone either
// way (typed name kept, blank name "Unknown"), and a device removed mid-query.
// The lookup is stubbed and parked on a channel so none of this races.
func TestHostnameLookup(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	win := a.NewWindow("PMS")
	pm := newAppState(win, fyne.NewStaticResource("trash.png", trashPNG))
	win.SetContent(pm.buildUI(fyne.NewStaticResource("ping-pong.png", pingPongPNG)))

	release := make(chan struct{})
	pm.lookupName = func(ip, iface string) string {
		<-release
		if ip == "10.0.0.1" {
			return "sw-core-01"
		}
		return ""
	}

	resolved := pm.addDevice("10.0.0.1", "")
	device := pm.devices[0]
	if device.Hostname != resolvingHostname {
		t.Errorf("hostname while resolving = %q, want %q", device.Hostname, resolvingHostname)
	}
	close(release)
	<-resolved
	if device.Hostname != "sw-core-01" {
		t.Errorf("resolved hostname = %q, want sw-core-01", device.Hostname)
	}
	if got := pm.rowFor(device).hostname.Text; got != "sw-core-01" {
		t.Errorf("resolved row label = %q, want sw-core-01", got)
	}
	// a resolved hostname does not become the device's name
	if device.Name != unknownName {
		t.Errorf("blank name after resolving = %q, want %q", device.Name, unknownName)
	}

	// nothing answered: the Hostname column says so rather than staying on the
	// placeholder
	<-pm.addDevice("10.0.0.2", "")
	if got := pm.devices[1].Hostname; got != emptyHostname {
		t.Errorf("unanswered hostname = %q, want %q", got, emptyHostname)
	}

	// a typed name is kept exactly as given; the SNMP answer only fills Hostname
	<-pm.addDevice("10.0.0.1", "Named")
	named := pm.devices[2]
	if named.Name != "Named" || named.Hostname != "sw-core-01" {
		t.Errorf("named device = %q / %q, want Named / sw-core-01", named.Name, named.Hostname)
	}
	if got := pm.rowFor(named).name.Text; got != "Named" {
		t.Errorf("named row label = %q, want Named", got)
	}

	// removing a device mid-query drops the answer instead of panicking
	pm.removeDevice(2)
	pm.applyResolvedHostname(named, "too-late")
	if named.Hostname != "sw-core-01" {
		t.Errorf("removed device's hostname was overwritten with %q", named.Hostname)
	}
}

// serialRestore restores the saved device list and lets the hostname lookups it
// starts answer one at a time, in list order, returning once they all have.
//
// The one-at-a-time part is a test-driver detail. The real app starts every
// lookup at once and fyne.Do funnels the answers onto the single UI goroutine;
// test.NewApp's driver instead runs fyne.Do inline on whichever goroutine calls
// it, so letting the answers overlap would put several goroutines inside Fyne's
// widget code simultaneously — something the app never does, and which -race
// reports (in Fyne's own font cache, not in this package).
func serialRestore(pm *appState, hostnames map[string]string) {
	// Built before any lookup starts and never written again, so the parked
	// goroutines only ever read it.
	gates := make(map[string]chan struct{}, len(hostnames))
	for ip := range hostnames {
		gates[ip] = make(chan struct{})
	}
	pm.lookupName = func(ip, iface string) string {
		if gate, ok := gates[ip]; ok {
			<-gate
		}
		return hostnames[ip]
	}

	chans := pm.restoreDevices()
	released := make(map[string]bool, len(chans))
	for i, done := range chans {
		ip := pm.devices[i].IP
		// chans is in device order, so this releases exactly the lookup that
		// `done` belongs to. A repeated IP shares its gate and is already through.
		if gate, ok := gates[ip]; ok && !released[ip] {
			released[ip] = true
			close(gate)
		}
		<-done
	}
}

// TestConfigPersistence covers the saved device list: that the list is written
// on every change to it (add, sort, remove, drag), that a second run gets the
// same devices back in the same order, and that counters and hostnames are *not*
// restored — counters belong to one run, and a hostname is re-asked so a device
// that has changed or gone away isn't shown a remembered name.
func TestConfigPersistence(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	path := filepath.Join(t.TempDir(), "pms", "config.json")

	// What each device answers when asked for its SNMP hostname: 10.0.0.2 has
	// one, the others don't (so they end up on "Empty").
	hostnames := map[string]string{"10.0.0.1": "", "10.0.0.2": "sw-core-02", "10.0.0.3": ""}

	newSession := func() *appState {
		win := a.NewWindow("PMS")
		pm := newAppState(win, fyne.NewStaticResource("trash.png", trashPNG))
		win.SetContent(pm.buildUI(fyne.NewStaticResource("ping-pong.png", pingPongPNG)))
		pm.configFile = path
		pm.lookupName = func(ip, iface string) string { return hostnames[ip] }
		return pm
	}

	// A run with no config file yet is a first run, not an error.
	first := newSession()
	if got := first.restoreDevices(); got != nil {
		t.Errorf("restore with no config file returned %d lookups, want none", len(got))
	}
	if len(first.devices) != 0 {
		t.Fatalf("restore with no config file added %d devices", len(first.devices))
	}

	<-first.addDevice("10.0.0.3", "Gamma")
	<-first.addDevice("10.0.0.2", "")
	<-first.addDevice("10.0.0.1", "Alpha")
	first.devices[0].Success, first.devices[0].Total = 7, 7
	first.sortBy(sortIP) // the order the second run has to come back in

	// Reopening the app: same devices, same order, names kept.
	second := newSession()
	serialRestore(second, hostnames)
	wantIPs := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	if len(second.devices) != len(wantIPs) {
		t.Fatalf("restored %d devices, want %d", len(second.devices), len(wantIPs))
	}
	for i, ip := range wantIPs {
		if got := second.devices[i].IP; got != ip {
			t.Errorf("restored device %d = %q, want %q", i, got, ip)
		}
		if second.rows[i].device != second.devices[i] {
			t.Errorf("restored row %d is not bound to device %d", i, i)
		}
	}
	if got := second.devices[0].Name; got != "Alpha" {
		t.Errorf("restored name = %q, want Alpha", got)
	}
	// a device added with a blank name comes back as "Unknown", same as it went
	if got := second.devices[1].Name; got != unknownName {
		t.Errorf("restored blank name = %q, want %q", got, unknownName)
	}
	// hostnames are re-resolved, not read from the file
	if got := second.devices[1].Hostname; got != "sw-core-02" {
		t.Errorf("re-resolved hostname = %q, want sw-core-02", got)
	}
	if got := second.rowFor(second.devices[1]).hostname.Text; got != "sw-core-02" {
		t.Errorf("restored row hostname = %q, want sw-core-02", got)
	}
	if got := second.devices[2].Hostname; got != emptyHostname {
		t.Errorf("unanswered restored hostname = %q, want %q", got, emptyHostname)
	}
	// counters are a per-run measurement and start clean
	for i, d := range second.devices {
		if d.Total != 0 || d.Success != 0 || d.Fail != 0 {
			t.Errorf("restored device %d carried counters %d/%d/%d", i, d.Success, d.Fail, d.Total)
		}
	}

	// a removal is saved too, so it doesn't come back on the next run
	second.removeDevice(1)
	third := newSession()
	serialRestore(third, hostnames)
	if len(third.devices) != 2 || third.devices[1].IP != "10.0.0.3" {
		t.Errorf("after remove, restored %v", []string{third.devices[0].IP, third.devices[len(third.devices)-1].IP})
	}

	// a drag saves the hand-made order
	third.dragRow(third.devices[0], third.rowStride())
	third.endRowDrag()
	fourth := newSession()
	serialRestore(fourth, hostnames)
	if fourth.devices[0].IP != "10.0.0.3" {
		t.Errorf("dragged order not saved, first = %q, want 10.0.0.3", fourth.devices[0].IP)
	}

	// with no config file set nothing is written and nothing is read: this is
	// what keeps every other test off the user's real config
	off := newSession()
	off.configFile = ""
	<-off.addDevice("10.9.9.9", "Nowhere")
	if got := off.restoreDevices(); got != nil {
		t.Errorf("restore with persistence off returned %d lookups", len(got))
	}
	saved, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	for _, d := range saved.Devices {
		if d.IP == "10.9.9.9" {
			t.Errorf("device was written despite persistence being off")
		}
	}
}

// TestConfigFile covers the file layer on its own: the round trip, the
// atomic-replace behaviour, and unreadable input.
func TestConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.json")

	// A missing file reads as an empty config, so a first run needs no special case.
	cfg, err := loadConfig(path)
	if err != nil || len(cfg.Devices) != 0 {
		t.Fatalf("missing file: %v, %d devices", err, len(cfg.Devices))
	}

	want := savedConfig{Devices: []savedDevice{{IP: "10.0.0.1", Name: "Alpha"}, {IP: "10.0.0.2", Name: unknownName}}}
	if err := saveConfig(path, want); err != nil { // creates the directory too
		t.Fatalf("saveConfig: %v", err)
	}
	got, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(got.Devices) != 2 || got.Devices[0] != want.Devices[0] || got.Devices[1] != want.Devices[1] {
		t.Errorf("round trip = %+v, want %+v", got.Devices, want.Devices)
	}

	// A second save replaces the first, and leaves no temp file behind.
	if err := saveConfig(path, savedConfig{Devices: []savedDevice{{IP: "10.0.0.9", Name: "Only"}}}); err != nil {
		t.Fatalf("second saveConfig: %v", err)
	}
	got, err = loadConfig(path)
	if err != nil || len(got.Devices) != 1 || got.Devices[0].IP != "10.0.0.9" {
		t.Errorf("after replace = %+v (%v)", got.Devices, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("config dir holds %v, want just config.json", names)
	}

	// Garbage is reported, not silently treated as an empty list — the caller
	// leaves the file alone instead of overwriting whatever is in there.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Errorf("corrupt config file did not report an error")
	}
}

// TestHostnameRefreshOnStart covers re-asking every device for its SNMP sysName
// when a run starts: a device renamed (or replaced, or only now reachable) since
// it was added shows its current name, the column goes back to the placeholder
// while the query is out, and an answer from before the refresh is dropped
// instead of landing on top of the new one.
func TestHostnameRefreshOnStart(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	win := a.NewWindow("PMS")
	pm := newAppState(win, fyne.NewStaticResource("trash.png", trashPNG))
	win.SetContent(pm.buildUI(fyne.NewStaticResource("ping-pong.png", pingPongPNG)))
	// No ping cycle is wanted here: the ticker Start kicks off must not reach a
	// tick and fork ping at real addresses while the test runs.
	pm.pingInterval = 3600

	pm.lookupName = func(ip, iface string) string { return "sw-core-01" }
	<-pm.addDevice("10.0.0.1", "Alpha")
	device := pm.devices[0]
	if device.Hostname != "sw-core-01" {
		t.Fatalf("hostname after add = %q, want sw-core-01", device.Hostname)
	}

	// The device is renamed on the far end; starting a run has to notice.
	release := make(chan struct{})
	pm.lookupName = func(ip, iface string) string {
		<-release
		return "sw-core-01-renamed"
	}
	done := pm.start()
	if device.Hostname != resolvingHostname {
		t.Errorf("hostname while re-resolving = %q, want %q", device.Hostname, resolvingHostname)
	}
	if got := pm.rowFor(device).hostname.Text; got != resolvingHostname {
		t.Errorf("row label while re-resolving = %q, want %q", got, resolvingHostname)
	}
	close(release)
	for _, ch := range done {
		<-ch
	}
	if device.Hostname != "sw-core-01-renamed" {
		t.Errorf("hostname after Start = %q, want sw-core-01-renamed", device.Hostname)
	}
	if got := pm.rowFor(device).hostname.Text; got != "sw-core-01-renamed" {
		t.Errorf("row label after Start = %q, want sw-core-01-renamed", got)
	}
	pm.stop()

	// A lookup parked from before a refresh must not overwrite the refresh's
	// answer when it finally comes back — a Stop/Start while a query is still
	// out on a dead host.
	parkedRelease := make(chan struct{})
	pm.lookupName = func(ip, iface string) string {
		<-parkedRelease
		return "" // an unanswered query, which would read "Empty"
	}
	parked := pm.refreshHostnames()

	pm.lookupName = func(ip, iface string) string { return "sw-core-02" }
	for _, ch := range pm.refreshHostnames() {
		<-ch
	}
	if device.Hostname != "sw-core-02" {
		t.Fatalf("hostname after second refresh = %q, want sw-core-02", device.Hostname)
	}

	close(parkedRelease)
	for _, ch := range parked {
		<-ch
	}
	if device.Hostname != "sw-core-02" {
		t.Errorf("superseded lookup overwrote the hostname with %q", device.Hostname)
	}

	// A refresh with an empty list is a no-op, not a panic.
	pm.removeDevice(0)
	if got := pm.refreshHostnames(); len(got) != 0 {
		t.Errorf("refresh with no devices started %d lookups", len(got))
	}
}

func TestParseSysName(t *testing.T) {
	cases := map[string]string{
		"sw-core-01\n":   "sw-core-01",
		"tahir-Dell-G15": "tahir-Dell-G15",
		"SNMPv2-MIB::sysName.0 = STRING: sw-core-01\n":     "sw-core-01",
		"iso.3.6.1.2.1.1.5.0 = STRING: \"sw core 01\"\n":   "sw core 01",
		"SNMPv2-MIB::sysName.0 = No Such Object available": "",
		"Timeout: No Response from 10.0.0.1.":              "",
		"":                                                 "",
		"first line\nsecond line":                          "first line",
	}
	for out, want := range cases {
		if got := parseSysName(out); got != want {
			t.Errorf("parseSysName(%q) = %q, want %q", out, got, want)
		}
	}

	long := strings.Repeat("x", maxSysNameLen+20)
	if got := parseSysName(long); len(got) != maxSysNameLen {
		t.Errorf("overlong sysName kept %d chars, want %d", len(got), maxSysNameLen)
	}
}

// TestInterfaceSourceIP pins the source-address choice for the SNMP query: it
// has to be the interface address on the *target's* subnet, not simply the
// interface's first address — an interface carrying several subnets (a normal
// setup on the machines this app watches) otherwise gets bound to a source the
// device cannot reply to, and every lookup silently times out. Uses lo, the one
// interface whose addressing is the same everywhere.
func TestInterfaceSourceIP(t *testing.T) {
	if _, err := net.InterfaceByName("lo"); err != nil {
		t.Skip("no lo interface")
	}

	// 127.0.0.1/8 covers the whole 127.x range
	if got := interfaceSourceIP("lo", "127.0.0.9"); got != "127.0.0.1" {
		t.Errorf("on-subnet source = %q, want 127.0.0.1", got)
	}
	// off-subnet: leave the source to the kernel's route lookup
	if got := interfaceSourceIP("lo", "10.0.0.3"); got != "" {
		t.Errorf("off-subnet source = %q, want empty", got)
	}
	if got := interfaceSourceIP("nosuchiface0", "127.0.0.1"); got != "" {
		t.Errorf("unknown interface source = %q, want empty", got)
	}
	if got := interfaceSourceIP("lo", "not-an-ip"); got != "" {
		t.Errorf("unparseable target source = %q, want empty", got)
	}
}

// TestPingWaitArg pins the -W formatting: a sub-second timeout has to reach
// ping as a fraction, not get truncated to whole seconds.
func TestPingWaitArg(t *testing.T) {
	cases := map[int]string{1000: "1", 1500: "1.5", 900: "0.9", 300: "0.3", 100: "0.1", 0: "0.001"}
	for ms, want := range cases {
		if got := pingWaitArg(ms); got != want {
			t.Errorf("pingWaitArg(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestStatusLine(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	win := a.NewWindow("PMS")
	pm := newAppState(win, fyne.NewStaticResource("trash.png", trashPNG))
	win.SetContent(pm.buildUI(fyne.NewStaticResource("ping-pong.png", pingPongPNG)))

	pm.lookupName = func(ip, iface string) string { return "" }

	if got := pm.statusLabel.Text; got != "No devices" {
		t.Errorf("empty status = %q", got)
	}

	<-pm.addDevice("10.0.0.1", "a")
	if got := pm.statusLabel.Text; got != "Stopped  ·  1 device" {
		t.Errorf("stopped status = %q", got)
	}

	<-pm.addDevice("10.0.0.2", "b")
	<-pm.addDevice("10.0.0.3", "c")
	pm.running = true
	pm.devices[0].Total, pm.devices[0].LastResult = 3, true
	pm.devices[1].Total, pm.devices[1].LastResult = 3, false
	// devices[2] never pinged -> pending, not "down"
	pm.refreshStatus()
	want := "Monitoring  ·  3 devices  ·  1 up  ·  1 down  ·  1 pending"
	if got := pm.statusLabel.Text; got != want {
		t.Errorf("running status = %q, want %q", got, want)
	}
}
