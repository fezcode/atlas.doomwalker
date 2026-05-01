//go:build !windows

package main

import "os"

func defaultTarget() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/"
}

// Non-Windows: there is no UAC. Treat as admin so the scan path proceeds; the
// MFT scanner is unreachable here anyway because we always go through walker.
func isAdmin() bool   { return true }
func elevate() error  { return nil }
