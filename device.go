package main

import "time"

// Device is one monitored target and its running ping statistics. Name is what
// the user typed (blank is allowed and stays blank); Hostname is what the
// device called itself over SNMP, resolved in the background for every device.
//
// Total counts echo requests *sent*, not results collected: one goes out per
// device per interval, so every device's Total advances at the same instant and
// stays level with the others' no matter how the devices differ in
// reachability. Success and Fail are the outcomes of those requests and can
// only follow later — a reply arrives when it arrives, and a request can only
// be called failed once its timeout has expired.
type Device struct {
	IP         string
	Name       string
	Hostname   string
	Success    int
	Fail       int
	Total      int
	LastResult bool

	// failHeldUntil is how long a failure keeps the row reading as failed even
	// after a later request has been answered. Without it a lost packet is
	// almost invisible: a failure is only known one timeout after its request
	// went out, while the *next* request goes out one interval after it and is
	// answered in about a millisecond — so at the default 1s interval and
	// 1000ms timeout the row turned red and green again within the same frame or
	// two. Written by applyProbeResult, read through showsFail; run state like
	// the counters, so it is not persisted.
	failHeldUntil time.Time
}

// showsFail reports whether the device should read as failed at time now: its
// last outcome was a failure, or a recent failure is still being held on screen.
// It is only about presentation — the Fail and Loss columns count every failure
// the moment it is known, held or not.
func (d *Device) showsFail(now time.Time) bool {
	return !d.LastResult || now.Before(d.failHeldUntil)
}

// Resolved is how many of the sent requests have an outcome. Total - Resolved
// is what is still in flight, which is why Loss is measured against this and
// not against Total: dividing by Total would dip the figure every time a
// request goes out and snap it back when the result landed.
func (d *Device) Resolved() int { return d.Success + d.Fail }
