package main

import (
	"context"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// maxConcurrentPings caps how many `ping` subprocesses run at once. A cycle is
// meant to probe every device at the same instant, so this is a backstop
// against forking absurd numbers of processes, not a throttle. It used to be 10
// (copied from the Python version's QThreadPool) — which meant a 40-device list
// needed four ~1s waves to finish one cycle, and on screen that read as the
// devices being pinged one after another.
const maxConcurrentPings = 256

// pingWaitArg renders timeoutMs as ping's -W value. -W takes fractional
// seconds, so sub-second timeouts are honoured as set; integer-truncating to
// whole seconds (with a 1s floor) used to make the whole 100-1000ms half of the
// settings range cost a full second per unanswered host.
func pingWaitArg(timeoutMs int) string {
	if timeoutMs < 1 {
		timeoutMs = 1
	}
	return strconv.FormatFloat(float64(timeoutMs)/1000, 'f', -1, 64)
}

// pingOne sends a single ICMP echo and reports whether a reply came back.
func pingOne(ip, iface string, timeoutMs int) bool {
	if timeoutMs < 1 {
		timeoutMs = 1
	}

	// The context is only a backstop for a ping that outlives its own -W; the
	// margin has to cover process spawn, so it can't be tight.
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(timeoutMs)*time.Millisecond+2*time.Second)
	defer cancel()

	// -n skips reverse-DNS resolution of the target: pure latency here, since
	// ping's output is discarded either way.
	cmd := exec.CommandContext(ctx, "ping", "-n", "-I", iface, "-c", "1", "-W", pingWaitArg(timeoutMs), ip)
	return cmd.Run() == nil
}

// runCycle pings every device concurrently (bounded by maxConcurrentPings),
// invoking onResult as each device's ping completes and onDone once all have.
// onResult identifies the device by pointer, not by list position: the UI can
// reorder its list (sort, drag) while a cycle is still in flight.
func runCycle(devices []*Device, iface string, timeoutMs int, onResult func(d *Device), onDone func()) {
	sem := make(chan struct{}, maxConcurrentPings)
	var wg sync.WaitGroup

	for _, device := range devices {
		wg.Add(1)
		// The semaphore is taken inside the goroutine, not in this loop: taking
		// it here serialised the process spawns behind each other, staggering
		// the start of a large cycle by milliseconds per device.
		go func(d *Device) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ok := pingOne(d.IP, iface, timeoutMs)
			d.Total++
			d.LastResult = ok
			if ok {
				d.Success++
			} else {
				d.Fail++
			}
			onResult(d)
		}(device)
	}

	go func() {
		wg.Wait()
		onDone()
	}()
}
