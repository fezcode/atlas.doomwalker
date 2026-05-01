package mft

// FileNode represents a file or directory in the scanned tree. It is the
// canonical type the UI and web server consume — kept in this package only
// for backwards compatibility with the original layout; both the MFT scanner
// (Windows-only) and the portable walker produce *FileNode.
type FileNode struct {
	Name     string
	Size     int64
	IsDir    bool
	Children map[string]*FileNode
	Parent   *FileNode
}

// Scanner reads the NTFS Master File Table for a given drive letter on
// Windows. On non-Windows platforms its Scan method returns an error and you
// should use the walker package instead.
type Scanner struct {
	Drive string
}

func NewScanner(drive string) *Scanner { return &Scanner{Drive: drive} }
