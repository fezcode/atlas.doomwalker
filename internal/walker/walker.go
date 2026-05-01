// Package walker is a cross-platform fallback that walks the filesystem with
// filepath.WalkDir. Slower than the NTFS MFT path but works on any OS / FS.
package walker

import (
	"io/fs"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"atlas.doomwalker/internal/mft"
)

// Scan walks `root` and produces a FileNode tree. Status and progress are
// reported through pChan exactly like the MFT scanner: float64 for progress
// (here a heartbeat — we can't know total files in advance) and string for
// status messages.
func Scan(root string, pChan chan<- any) (*mft.FileNode, error) {
	root = filepath.Clean(root)
	send(pChan, "Walking "+root)

	rootName := root
	if v := filepath.VolumeName(root); v != "" && v == strings.TrimSuffix(root, string(filepath.Separator)) {
		rootName = v // "C:" rather than "C:\"
	}
	tree := &mft.FileNode{Name: rootName, IsDir: true, Children: map[string]*mft.FileNode{}}

	// Map absolute path -> node so we can attach children in O(1).
	index := map[string]*mft.FileNode{root: tree}

	var count atomic.Int64
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				// Heartbeat progress — oscillates so the bar visibly moves.
				n := count.Load()
				p := float64(n%100000) / 100000.0
				send(pChan, p)
			}
		}
	}()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip permission errors etc.
		}
		count.Add(1)
		if path == root {
			return nil
		}
		parentPath := filepath.Dir(path)
		parent, ok := index[parentPath]
		if !ok {
			return nil // shouldn't happen with WalkDir's order, but be safe
		}
		isDir := d.IsDir()
		var size int64
		if !isDir {
			info, err := d.Info()
			if err == nil {
				size = info.Size()
			}
		}
		node := &mft.FileNode{
			Name:     d.Name(),
			Size:     size,
			IsDir:    isDir,
			Parent:   parent,
			Children: map[string]*mft.FileNode{},
		}
		// Disambiguate hardlinks / case collisions.
		key := node.Name
		if _, dup := parent.Children[key]; dup {
			key = key + " [#" + filepath.Base(path) + "]"
		}
		parent.Children[key] = node
		if isDir {
			index[path] = node
		}
		return nil
	})
	close(stop)
	if err != nil {
		return nil, err
	}

	send(pChan, "Computing sizes")
	finalize(tree)
	send(pChan, 1.0)
	return tree, nil
}

func finalize(root *mft.FileNode) {
	stack := []*mft.FileNode{root}
	order := make([]*mft.FileNode, 0, 1024)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, n)
		for _, c := range n.Children {
			if c.IsDir {
				stack = append(stack, c)
			}
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		if !n.IsDir {
			continue
		}
		var total int64
		for _, c := range n.Children {
			total += c.Size
		}
		n.Size = total
	}
}

func send(ch chan<- any, v any) {
	if ch == nil {
		return
	}
	select {
	case ch <- v:
	default:
	}
}
