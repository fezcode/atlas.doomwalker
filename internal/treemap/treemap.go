package treemap

import "sort"

// Rect is a placed rectangle in the treemap grid.
type Rect struct {
	X, Y, W, H int
	Label      string
	Size       int64
}

// Node is an input item.
type Node struct {
	Label string
	Size  int64
}

// SquarifiedTreemap returns rectangles laid out with the squarified algorithm
// of Bruls, Huijsen & van Wijk (2000). Computation is in float space; cells
// snap to the integer grid only at emit time so they tile exactly.
func SquarifiedTreemap(nodes []Node, x, y, w, h int) []Rect {
	if w <= 0 || h <= 0 || len(nodes) == 0 {
		return nil
	}
	in := make([]Node, 0, len(nodes))
	var total int64
	for _, n := range nodes {
		if n.Size <= 0 {
			continue
		}
		in = append(in, n)
		total += n.Size
	}
	if len(in) == 0 || total == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Size > in[j].Size })

	scale := float64(w) * float64(h) / float64(total)
	areas := make([]float64, len(in))
	for i, n := range in {
		areas[i] = float64(n.Size) * scale
	}

	var out []Rect
	rx, ry, rw, rh := float64(x), float64(y), float64(w), float64(h)

	i := 0
	for i < len(in) {
		// Grow a row from index i while aspect ratio improves.
		j := i + 1
		for j < len(in) {
			cur := areas[i:j]
			withNext := areas[i : j+1]
			if worst(withNext, minF(rw, rh)) > worst(cur, minF(rw, rh)) {
				break
			}
			j++
		}
		rx, ry, rw, rh = layoutRow(in[i:j], areas[i:j], rx, ry, rw, rh, &out)
		i = j
	}
	return out
}

// worst returns the worst aspect ratio in `row` when laid along shorter side `side`.
func worst(row []float64, side float64) float64 {
	if side <= 0 || len(row) == 0 {
		return 1e18
	}
	var sum, rmax, rmin float64
	rmin = 1e18
	for _, a := range row {
		sum += a
		if a > rmax {
			rmax = a
		}
		if a < rmin {
			rmin = a
		}
	}
	if sum == 0 || rmin == 0 {
		return 1e18
	}
	s2 := side * side
	return maxF((s2*rmax)/(sum*sum), (sum*sum)/(s2*rmin))
}

// layoutRow emits one strip of rectangles and returns the remaining sub-rect.
func layoutRow(nodes []Node, areas []float64, x, y, w, h float64, out *[]Rect) (float64, float64, float64, float64) {
	var sum float64
	for _, a := range areas {
		sum += a
	}
	if sum == 0 {
		return x, y, w, h
	}
	if w <= h {
		stripH := sum / w
		cx := x
		for i, a := range areas {
			rw := a / stripH
			x0, x1 := int(cx+0.5), int(cx+rw+0.5)
			y0, y1 := int(y+0.5), int(y+stripH+0.5)
			*out = append(*out, Rect{
				X: x0, Y: y0, W: x1 - x0, H: y1 - y0,
				Label: nodes[i].Label, Size: nodes[i].Size,
			})
			cx += rw
		}
		return x, y + stripH, w, h - stripH
	}
	stripW := sum / h
	cy := y
	for i, a := range areas {
		rh := a / stripW
		x0, x1 := int(x+0.5), int(x+stripW+0.5)
		y0, y1 := int(cy+0.5), int(cy+rh+0.5)
		*out = append(*out, Rect{
			X: x0, Y: y0, W: x1 - x0, H: y1 - y0,
			Label: nodes[i].Label, Size: nodes[i].Size,
		})
		cy += rh
	}
	return x + stripW, y, w - stripW, h
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
