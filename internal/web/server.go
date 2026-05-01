// Package web serves a browser-based UI over the scanned MFT tree.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"atlas.doomwalker/internal/mft"
)

//go:embed static
var staticFS embed.FS

// Serve scans, then runs the HTTP UI at addr until interrupted.
func Serve(addr string, root *mft.FileNode) error {
	s := &server{root: root, ids: map[uint64]*mft.FileNode{}, rev: map[*mft.FileNode]uint64{}}
	s.assign(root)

	mux := http.NewServeMux()

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		b, _ := fs.ReadFile(sub, "index.html")
		w.Write(b)
	})
	mux.HandleFunc("/api/node", s.handleNode)
	mux.HandleFunc("/api/root", s.handleRoot)

	fmt.Printf("\n  ▸ Open http://%s in your browser\n  ▸ Ctrl+C to stop\n\n", addr)
	return http.ListenAndServe(addr, mux)
}

type server struct {
	root *mft.FileNode

	mu     sync.Mutex
	idSeq  atomic.Uint64
	ids    map[uint64]*mft.FileNode
	rev    map[*mft.FileNode]uint64
}

func (s *server) assign(n *mft.FileNode) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.rev[n]; ok {
		return id
	}
	id := s.idSeq.Add(1)
	s.ids[id] = n
	s.rev[n] = id
	return id
}

func (s *server) lookup(id uint64) *mft.FileNode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ids[id]
}

type childDTO struct {
	ID       uint64 `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"isDir"`
	HasMore  bool   `json:"hasMore,omitempty"`
}

type nodeDTO struct {
	ID         uint64     `json:"id"`
	Name       string     `json:"name"`
	Size       int64      `json:"size"`
	IsDir      bool       `json:"isDir"`
	Path       []pathSeg  `json:"path"`
	Children   []childDTO `json:"children"`
	TotalCount int        `json:"totalCount"`
}

type pathSeg struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	s.writeNode(w, s.root)
}

func (s *server) handleNode(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseUint(idStr, 10, 64)
	n := s.lookup(id)
	if n == nil {
		http.Error(w, "unknown id", http.StatusNotFound)
		return
	}
	s.writeNode(w, n)
}

func (s *server) writeNode(w http.ResponseWriter, n *mft.FileNode) {
	const maxChildren = 250

	kids := make([]*mft.FileNode, 0, len(n.Children))
	for _, c := range n.Children {
		if strings.HasPrefix(c.Name, "$") {
			continue
		}
		kids = append(kids, c)
	}
	sort.Slice(kids, func(i, j int) bool { return kids[i].Size > kids[j].Size })
	total := len(kids)
	hasMore := false
	if total > maxChildren {
		kids = kids[:maxChildren]
		hasMore = true
	}

	out := nodeDTO{
		ID:         s.assign(n),
		Name:       n.Name,
		Size:       n.Size,
		IsDir:      n.IsDir,
		TotalCount: total,
	}
	for cur := n; cur != nil; cur = cur.Parent {
		out.Path = append([]pathSeg{{ID: s.assign(cur), Name: cur.Name}}, out.Path...)
	}
	for i, c := range kids {
		dto := childDTO{
			ID: s.assign(c), Name: c.Name, Size: c.Size, IsDir: c.IsDir,
		}
		if i == len(kids)-1 && hasMore {
			dto.HasMore = true
		}
		out.Children = append(out.Children, dto)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(out)
}
