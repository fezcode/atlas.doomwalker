//go:build windows

package mft

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"github.com/t9t/gomft/mft"
	"golang.org/x/sys/windows"
)

// nodeInfo is the flat record we accumulate during the MFT pass.
type nodeInfo struct {
	Name      string
	ParentFRN uint64
	Size      int64
	IsDir     bool
}

func (s *Scanner) Scan(pChan chan<- any) (*FileNode, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("MFT scanning is only supported on Windows")
	}

	drive := strings.TrimSuffix(s.Drive, ":")
	if len(drive) == 0 {
		return nil, fmt.Errorf("invalid drive: %q", s.Drive)
	}
	volumePath := `\\.\` + drive + `:`

	h, err := windows.CreateFile(
		windows.StringToUTF16Ptr(volumePath),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open volume %s: %w (need admin?)", volumePath, err)
	}
	// Wrap the handle in *os.File so we get ReadAt without a second CreateFile.
	f := os.NewFile(uintptr(h), volumePath)
	defer f.Close()

	var volumeData NTFS_VOLUME_DATA_BUFFER
	var bytesReturned uint32
	if err := windows.DeviceIoControl(
		h,
		FSCTL_GET_NTFS_VOLUME_DATA,
		nil, 0,
		(*byte)(unsafe.Pointer(&volumeData)),
		uint32(unsafe.Sizeof(volumeData)),
		&bytesReturned, nil,
	); err != nil {
		return nil, fmt.Errorf("FSCTL_GET_NTFS_VOLUME_DATA failed: %w", err)
	}

	send(pChan, "Locating MFT fragments")
	mftOffset := volumeData.MftStartLcn * int64(volumeData.BytesPerCluster)
	recordSize := int(volumeData.BytesPerFileRecord)
	if recordSize <= 0 || recordSize > 64*1024 {
		return nil, fmt.Errorf("unexpected MFT record size: %d", recordSize)
	}

	firstRecord := make([]byte, recordSize)
	if _, err := f.ReadAt(firstRecord, mftOffset); err != nil {
		return nil, fmt.Errorf("read MFT[0]: %w", err)
	}
	rec0, err := mft.ParseRecord(firstRecord)
	if err != nil {
		return nil, fmt.Errorf("parse MFT[0]: %w", err)
	}

	var dataRuns []mft.DataRun
	for _, attr := range rec0.Attributes {
		if attr.Type == mft.AttributeTypeData && !attr.Resident {
			dataRuns, err = mft.ParseDataRuns(attr.Data)
			if err != nil {
				return nil, fmt.Errorf("parse MFT data runs: %w", err)
			}
			break
		}
	}
	if len(dataRuns) == 0 {
		return nil, fmt.Errorf("MFT has no non-resident DATA runs")
	}

	send(pChan, "Reading MFT")
	const chunkSize = 1 << 20
	buffer := make([]byte, chunkSize)
	nodes := make(map[uint64]*nodeInfo, 1<<16)

	var totalMftBytes int64
	for _, run := range dataRuns {
		totalMftBytes += int64(run.LengthInClusters) * int64(volumeData.BytesPerCluster)
	}

	var bytesRead int64
	var currentLcn int64
	for _, run := range dataRuns {
		currentLcn += run.OffsetCluster
		runOffset := currentLcn * int64(volumeData.BytesPerCluster)
		runLength := int64(run.LengthInClusters) * int64(volumeData.BytesPerCluster)

		for offset := int64(0); offset < runLength; offset += int64(chunkSize) {
			readSize := int64(chunkSize)
			if offset+readSize > runLength {
				readSize = runLength - offset
			}
			// Round down to a whole number of records so we never slice past the end.
			usable := (readSize / int64(recordSize)) * int64(recordSize)
			if usable == 0 {
				break
			}
			if _, err := f.ReadAt(buffer[:readSize], runOffset+offset); err != nil {
				break
			}
			bytesRead += readSize
			sendProgress(pChan, float64(bytesRead)/float64(totalMftBytes))

			for rOffset := int64(0); rOffset+int64(recordSize) <= usable; rOffset += int64(recordSize) {
				recordBuf := buffer[rOffset : rOffset+int64(recordSize)]
				if string(recordBuf[:4]) != "FILE" {
					continue
				}
				record, err := mft.ParseRecord(recordBuf)
				if err != nil {
					continue
				}
				// Skip records not in use.
				if record.Flags&0x0001 == 0 {
					continue
				}

				var name string
				var parentFRN uint64
				var size, fnFallback int64
				bestNamespace := mft.FileNameNamespace(99)
				isDir := record.Flags&0x0002 != 0

				for _, attr := range record.Attributes {
					switch attr.Type {
					case mft.AttributeTypeFileName:
						fn, err := mft.ParseFileName(attr.Data)
						if err != nil {
							continue
						}
						// Prefer Win32/POSIX names over DOS short names.
						if name == "" || (fn.Namespace != mft.FileNameNamespaceDos && fn.Namespace < bestNamespace) {
							name = fn.Name
							parentFRN = fn.ParentFileReference.RecordNumber
							bestNamespace = fn.Namespace
						}
						// $FILE_NAME caches a size — useful when DATA lives in extension records.
						if s := int64(fn.AllocatedSize); s > fnFallback {
							fnFallback = s
						}
					case mft.AttributeTypeData:
						if attr.Resident {
							if int64(len(attr.Data)) > size {
								size = int64(len(attr.Data))
							}
						} else {
							s := int64(attr.AllocatedSize)
							if s == 0 {
								s = int64(attr.ActualSize)
							}
							if s > size {
								size = s
							}
						}
					}
				}
				if size == 0 && !isDir {
					size = fnFallback
				}

				if name == "" {
					continue
				}
				// Skip NTFS reserved metafiles (FRNs 0–15: $MFT, $MFTMirr,
				// $LogFile, $Volume, $AttrDef, $Bitmap, $Boot, $BadClus,
				// $Secure, $UpCase, $Extend, …). $BadClus in particular is
				// a sparse stream whose logical size equals the entire
				// volume, which would otherwise inflate the root total by
				// ~one volume size. User-visible $-prefixed entries like
				// $Recycle.Bin live above FRN 15 and are kept.
				if record.FileReference.RecordNumber < 16 {
					continue
				}
				nodes[record.FileReference.RecordNumber] = &nodeInfo{
					Name: name, ParentFRN: parentFRN, Size: size, IsDir: isDir,
				}
			}
		}
	}

	send(pChan, "Building tree")
	sendProgress(pChan, 0)
	root := &FileNode{Name: drive + ":", IsDir: true, Children: make(map[string]*FileNode)}
	tree := make(map[uint64]*FileNode, len(nodes))
	tree[5] = root // FRN 5 == root directory ".".

	for frn, info := range nodes {
		if frn == 5 {
			continue
		}
		tree[frn] = &FileNode{
			Name:     info.Name,
			Size:     info.Size,
			IsDir:    info.IsDir,
			Children: make(map[string]*FileNode),
		}
	}

	send(pChan, "Linking nodes")
	for frn, info := range nodes {
		if frn == 5 {
			continue
		}
		node := tree[frn]
		parent, ok := tree[info.ParentFRN]
		if !ok {
			parent = root // orphan: re-parent to root so its size still counts.
		}
		node.Parent = parent
		// Disambiguate same-name siblings (hardlinks/short-name collisions).
		if _, dup := parent.Children[node.Name]; dup {
			parent.Children[fmt.Sprintf("%s [#%d]", node.Name, frn)] = node
		} else {
			parent.Children[node.Name] = node
		}
	}

	send(pChan, "Computing sizes")
	sendProgress(pChan, 0)
	finalizeSize(root)
	sendProgress(pChan, 1)
	return root, nil
}

// finalizeSize walks the tree iteratively and sums directory sizes.
// Iterative to avoid blowing the stack on deep trees.
func finalizeSize(root *FileNode) {
	stack := []*FileNode{root}
	order := make([]*FileNode, 0, 1024)
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

func sendProgress(ch chan<- any, p float64) {
	send(ch, p)
}

const FSCTL_GET_NTFS_VOLUME_DATA = 0x00090064

type NTFS_VOLUME_DATA_BUFFER struct {
	VolumeSerialNumber    int64
	NumberSectors         int64
	TotalClusters         int64
	FreeClusters          int64
	TotalReserved         int64
	BytesPerSector        uint32
	BytesPerCluster       uint32
	BytesPerFileRecord    uint32
	ClustersPerFileRecord uint32
	MftValidDataLength    int64
	MftStartLcn           int64
	Mft2StartLcn          int64
	MftZoneStart          int64
	MftZoneEnd            int64
}
