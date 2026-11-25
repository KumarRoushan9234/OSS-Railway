//go:build !linux

package main

// diskUsage is only implemented on Linux, the platform the panel runs on.
func diskUsage(string) (int64, int64) { return 0, 0 }
