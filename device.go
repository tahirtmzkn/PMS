package main

// Device is one monitored target and its running ping statistics.
type Device struct {
	IP         string
	Name       string
	Success    int
	Fail       int
	Total      int
	LastResult bool
}
