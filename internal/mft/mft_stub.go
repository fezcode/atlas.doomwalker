//go:build !windows

package mft

import "fmt"

// Scan is unsupported on non-Windows platforms. Use the walker package.
func (s *Scanner) Scan(pChan chan<- any) (*FileNode, error) {
	return nil, fmt.Errorf("MFT scanning is only supported on Windows; use --walker")
}
