package main

import (
	"context"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// maxConcurrentPings caps how many `ping` subprocesses run at once per cycle,
// mirroring the Python version's QThreadPool(maxThreadCount=10).
const maxConcurrentPings = 10

func pingOne(ip, iface string, timeoutMs int) bool {
	timeoutSec := timeoutMs / 1000
	if timeoutSec < 1 {
		timeoutSec = 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec+1)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping", "-I", iface, "-c", "1", "-W", strconv.Itoa(timeoutSec), ip)
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
		sem <- struct{}{}
		go func(d *Device) {
			defer wg.Done()
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
