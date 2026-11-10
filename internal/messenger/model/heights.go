package model

// HeightIndex is a Fenwick tree over cached or estimated message heights.
// Set, Prefix and Find are O(log n); no off-screen text is measured.
type HeightIndex struct {
	heights []int
	tree    []int64
}

func NewHeightIndex(heights []int) *HeightIndex {
	h := &HeightIndex{heights: make([]int, len(heights)), tree: make([]int64, len(heights)+1)}
	for i, v := range heights {
		h.Set(i, v)
	}
	return h
}
func (h *HeightIndex) Set(i, v int) {
	if i < 0 || i >= len(h.heights) {
		return
	}
	v = max(v, 1)
	d := int64(v - h.heights[i])
	h.heights[i] = v
	for j := i + 1; j < len(h.tree); j += j & -j {
		h.tree[j] += d
	}
}
func (h *HeightIndex) Prefix(n int) int64 {
	n = max(0, min(n, len(h.heights)))
	var sum int64
	for n > 0 {
		sum += h.tree[n]
		n -= n & -n
	}
	return sum
}
func (h *HeightIndex) Total() int64 { return h.Prefix(len(h.heights)) }

// Len is how many heights the index has.
func (h *HeightIndex) Len() int { return len(h.heights) }
func (h *HeightIndex) Find(offset int64) (index, inside int) {
	if len(h.heights) == 0 {
		return 0, 0
	}
	offset = max(0, min(offset, h.Total()-1))
	var sum int64
	bit := 1
	for bit < len(h.tree) {
		bit <<= 1
	}
	for ; bit > 0; bit >>= 1 {
		next := index + bit
		if next < len(h.tree) && sum+h.tree[next] <= offset {
			index = next
			sum += h.tree[next]
		}
	}
	return index, int(offset - sum)
}
