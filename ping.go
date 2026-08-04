package main

import (
	"os/exec"
	"strconv"
	"time"
)

// maxConcurrentPings caps how many `ping` subprocesses run at once. A round is
// meant to probe every device at the same instant, so this is a backstop
// against forking absurd numbers of processes, not a throttle. It used to be 10
// (copied from the Python version's QThreadPool) — which meant a 40-device list
// needed four ~1s waves to finish one round, and on screen that read as the
// devices being pinged one after another.
const maxConcurrentPings = 256

// pingSem bounds concurrency across *all* rounds, not within one. Rounds
// overlap whenever the timeout outlasts the interval — a request goes out every
// interval regardless of whether the previous one has been answered — so a
// semaphore created per round would have stopped bounding anything.
var pingSem = make(chan struct{}, maxConcurrentPings)

// pingWaitArg renders timeoutMs as ping's -W value: whole seconds, rounded up,
// never below 1. It is deliberately *not* the real timeout — runBounded below
// enforces that to the millisecond — this is only a backstop for a ping that
// outlives its own budget, so a coarse value is all it has to be.
//
// It used to pass fractional seconds ("0.3"), which the iputils on Ubuntu 20.04
// and later honours exactly. Ubuntu 18.04's ping (iputils s20161105) instead
// reads -W with strtoul, so "0.3" arrives as 0 — and a 0 linger time makes it
// wait for a reply that never comes *forever*, not for the fraction of a second
// that was asked for (measured: still running after 12s). Rounding up is what
// makes one binary behave the same on every supported release: every ping
// understands whole seconds, and rounding up rather than down keeps this a
// backstop that can only fire after the real deadline, never before it.
func pingWaitArg(timeoutMs int) string {
	if timeoutMs < 1 {
		timeoutMs = 1
	}
	return strconv.Itoa((timeoutMs + 999) / 1000)
}

// runBounded starts cmd and gives it budget to run in, reporting cmd's own error
// or a non-nil error if the budget ran out and the process had to be killed.
//
// The budget is timed from after the fork/exec, not from before it, and that is
// the point of doing this by hand rather than with exec.CommandContext: the
// deadline is then the device's time to answer, rather than that time minus
// however long spawning a process happened to take. A margin added to a
// context deadline to cover process spawn is either too tight (a busy machine
// spawns slowly, the ping is killed before the device was ever given its full
// timeout, and a device that is up reads as down) or too loose (an unanswered
// host holds a cycle open for the margin, which is what the old 2s backstop
// did). Timing from process start needs no margin at all.
func runBounded(cmd *exec.Cmd, budget time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	// Kill, not a polite signal: the process is a single ping whose deadline has
	// passed, and there is nothing for it to clean up. Racing Wait below is
	// safe — os.Process marks itself done under a lock as it is reaped, so a
	// Kill that loses the race returns ErrProcessDone instead of signalling a
	// pid that has since been reused. (This is the same dance
	// exec.CommandContext does internally.)
	stop := time.AfterFunc(budget, func() { _ = cmd.Process.Kill() })
	defer stop.Stop()
	return cmd.Wait()
}

// pingOne sends a single ICMP echo and reports whether a reply came back.
func pingOne(ip, iface string, timeoutMs int) bool {
	if timeoutMs < 1 {
		timeoutMs = 1
	}

	// -n skips reverse-DNS resolution of the target: pure latency here, since
	// ping's output is discarded either way.
	cmd := exec.Command("ping", "-n", "-I", iface, "-c", "1", "-W", pingWaitArg(timeoutMs), ip)
	return runBounded(cmd, time.Duration(timeoutMs)*time.Millisecond) == nil
}

// probeDevices sends one echo request to every device at once (bounded by
// pingSem) and reports each outcome through onResult as it arrives.
//
// It deliberately does not touch the devices' counters. Rounds overlap, so two
// goroutines could otherwise be incrementing one device's Success at the same
// moment; instead every counter is written on the UI goroutine — Total by
// appState.tick as the requests go out, Success/Fail by appState.applyProbeResult
// once onResult has marshalled the outcome back. onResult identifies the device
// by pointer, not by list position, so the UI is free to sort, drag or remove
// while a request is still in flight.
// probe is normally pingOne; appState holds it as a field so tests can stub it.
func probeDevices(devices []*Device, iface string, timeoutMs int, probe func(ip, iface string, timeoutMs int) bool, onResult func(d *Device, ok bool)) {
	for _, device := range devices {
		// The semaphore is taken inside the goroutine, not in this loop: taking
		// it here serialised the process spawns behind each other, staggering
		// the start of a large round by milliseconds per device.
		go func(d *Device) {
			pingSem <- struct{}{}
			defer func() { <-pingSem }()

			onResult(d, probe(d.IP, iface, timeoutMs))
		}(device)
	}
}
