package main

// Device is one monitored target and its running ping statistics. Name is what
// the user typed (blank is allowed and stays blank); Hostname is what the
// device called itself over SNMP, resolved in the background for every device.
type Device struct {
	IP         string
	Name       string
	Hostname   string
	Success    int
	Fail       int
	Total      int
	LastResult bool
}
