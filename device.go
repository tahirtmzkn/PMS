package main

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
}

// Resolved is how many of the sent requests have an outcome. Total - Resolved
// is what is still in flight, which is why Loss is measured against this and
// not against Total: dividing by Total would dip the figure every time a
// request goes out and snap it back when the result landed.
func (d *Device) Resolved() int { return d.Success + d.Fail }
