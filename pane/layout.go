package pane

import (
	"image"
	"math"

	"github.com/studiowebux/zurm/config"
)

// NodeKind identifies the type of a LayoutNode.
type NodeKind int

const (
	Leaf   NodeKind = iota
	HSplit          // left | right (vertical divider)
	VSplit          // top / bottom (horizontal divider)
)


// LayoutNode is a binary tree node.
// Leaf nodes own a Pane; internal nodes own Left and Right subtrees.
// Pattern: composite tree — recursive rect assignment and traversal.
type LayoutNode struct {
	Kind  NodeKind
	Pane  *Pane        // non-nil for Leaf
	Left  *LayoutNode  // non-nil for HSplit/VSplit
	Right *LayoutNode  // non-nil for HSplit/VSplit
	Ratio float64      // fraction of space given to Left (default 0.5)
	Rect  image.Rectangle

	// cachedLeaves avoids re-allocating the leaves slice on every call.
	// Invalidated by InvalidateLeaves after tree mutations (split, remove).
	cachedLeaves []*LayoutNode
}

// NewLeaf wraps an existing Pane in a Leaf node.
func NewLeaf(p *Pane) *LayoutNode {
	return &LayoutNode{Kind: Leaf, Pane: p, Ratio: 0.5}
}

// ComputeRects recursively assigns Rect to every node and updates
// Cols/Rows on every Leaf pane based on its computed rect.
func (n *LayoutNode) ComputeRects(rect image.Rectangle, cellW, cellH, padding, dividerPx int) {
	n.Rect = rect
	switch n.Kind {
	case Leaf:
		n.Pane.Rect = rect
		cols := (rect.Dx() - padding*2) / cellW
		rows := (rect.Dy() - padding - n.Pane.HeaderH) / cellH
		if cols < 1 {
			cols = 1
		}
		if rows < 1 {
			rows = 1
		}
		n.Pane.Cols = cols
		n.Pane.Rows = rows

	case HSplit:
		// Split horizontally: Left | Right with a vertical divider.
		leftW := int(float64(rect.Dx()) * n.Ratio)
		if leftW < 1 {
			leftW = 1
		}
		rightX := rect.Min.X + leftW + dividerPx
		if rightX >= rect.Max.X {
			rightX = rect.Max.X - 1
		}
		leftRect := image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+leftW, rect.Max.Y)
		rightRect := image.Rect(rightX, rect.Min.Y, rect.Max.X, rect.Max.Y)
		n.Left.ComputeRects(leftRect, cellW, cellH, padding, dividerPx)
		n.Right.ComputeRects(rightRect, cellW, cellH, padding, dividerPx)

	case VSplit:
		// Split vertically: Top / Bottom with a horizontal divider.
		topH := int(float64(rect.Dy()) * n.Ratio)
		if topH < 1 {
			topH = 1
		}
		bottomY := rect.Min.Y + topH + dividerPx
		if bottomY >= rect.Max.Y {
			bottomY = rect.Max.Y - 1
		}
		topRect := image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+topH)
		bottomRect := image.Rect(rect.Min.X, bottomY, rect.Max.X, rect.Max.Y)
		n.Left.ComputeRects(topRect, cellW, cellH, padding, dividerPx)
		n.Right.ComputeRects(bottomRect, cellW, cellH, padding, dividerPx)
	}
}

// Leaves returns all leaf nodes in DFS order (left-to-right, top-to-bottom).
// The result is cached — call InvalidateLeaves after tree mutations.
func (n *LayoutNode) Leaves() []*LayoutNode {
	if n.cachedLeaves != nil {
		return n.cachedLeaves
	}
	if n.Kind == Leaf {
		n.cachedLeaves = []*LayoutNode{n}
		return n.cachedLeaves
	}
	result := make([]*LayoutNode, 0, 4)
	result = n.collectLeaves(result)
	n.cachedLeaves = result
	return result
}

// collectLeaves appends leaf nodes to dst without caching intermediate results.
func (n *LayoutNode) collectLeaves(dst []*LayoutNode) []*LayoutNode {
	if n.Kind == Leaf {
		return append(dst, n)
	}
	dst = n.Left.collectLeaves(dst)
	dst = n.Right.collectLeaves(dst)
	return dst
}

// InvalidateLeaves clears the cached leaves slice on this node and all ancestors.
// Call after any tree mutation (split, remove, detach, attach).
func (n *LayoutNode) InvalidateLeaves() {
	n.cachedLeaves = nil
	if n.Left != nil {
		n.Left.InvalidateLeaves()
	}
	if n.Right != nil {
		n.Right.InvalidateLeaves()
	}
}

// PaneAt returns the Pane whose Rect contains pixel (x, y), or nil.
func (n *LayoutNode) PaneAt(x, y int) *Pane {
	for _, leaf := range n.Leaves() {
		if image.Pt(x, y).In(leaf.Pane.Rect) {
			return leaf.Pane
		}
	}
	return nil
}


// SplitAt returns the deepest split node whose divider region contains the
// pixel coordinate (x, y). margin expands the hit zone around the 1px divider.
// Returns nil if no divider is hit.
func (n *LayoutNode) SplitAt(x, y, margin int) *LayoutNode {
	if n == nil || n.Kind == Leaf {
		return nil
	}
	// Check children first (deeper splits take priority).
	if hit := n.Left.SplitAt(x, y, margin); hit != nil {
		return hit
	}
	if hit := n.Right.SplitAt(x, y, margin); hit != nil {
		return hit
	}
	// Check this node's own divider.
	switch n.Kind {
	case HSplit:
		divX := n.Left.Rect.Max.X
		if x >= divX-margin && x <= n.Right.Rect.Min.X+margin &&
			y >= n.Rect.Min.Y && y <= n.Rect.Max.Y {
			return n
		}
	case VSplit:
		divY := n.Left.Rect.Max.Y
		if y >= divY-margin && y <= n.Right.Rect.Min.Y+margin &&
			x >= n.Rect.Min.X && x <= n.Rect.Max.X {
			return n
		}
	}
	return nil
}

// FindParent returns the parent split node whose Left or Right child is the
// leaf containing p. Returns (parent, isLeft) — isLeft=true if p is Left child.
func (n *LayoutNode) FindParent(p *Pane) (*LayoutNode, bool) {
	if n.Kind == Leaf {
		return nil, false
	}
	if n.Left.Kind == Leaf && n.Left.Pane == p {
		return n, true
	}
	if n.Right.Kind == Leaf && n.Right.Pane == p {
		return n, false
	}
	if parent, isLeft := n.Left.FindParent(p); parent != nil {
		return parent, isLeft
	}
	return n.Right.FindParent(p)
}

// splitWith is the common implementation for all split operations.
// kind selects HSplit or VSplit; createPane builds the new pane.
func (n *LayoutNode) splitWith(p *Pane, kind NodeKind, createPane func() (*Pane, error)) (*LayoutNode, *Pane, error) {
	newPane, err := createPane()
	if err != nil {
		return n, nil, err
	}
	oldLeaf := NewLeaf(p)
	newLeaf := NewLeaf(newPane)
	split := &LayoutNode{Kind: kind, Left: oldLeaf, Right: newLeaf, Ratio: 0.5}
	result := replaceLeaf(n, p, split)
	result.InvalidateLeaves()
	return result, newPane, nil
}

// SplitH splits the pane p horizontally (left | right), creating a new pane
// as the right child. Returns the new tree root and the new pane.
func (n *LayoutNode) SplitH(p *Pane, cfg *config.Config, cellW, cellH int, dir string) (*LayoutNode, *Pane, error) {
	return n.splitWith(p, HSplit, func() (*Pane, error) { return New(cfg, p.Rect, cellW, cellH, dir) })
}

// SplitV splits the pane p vertically (top / bottom), creating a new pane
// as the bottom child. Returns the new tree root and the new pane.
func (n *LayoutNode) SplitV(p *Pane, cfg *config.Config, cellW, cellH int, dir string) (*LayoutNode, *Pane, error) {
	return n.splitWith(p, VSplit, func() (*Pane, error) { return New(cfg, p.Rect, cellW, cellH, dir) })
}

// SplitHServer is like SplitH but the new pane is backed by zurm-server (Mode B).
func (n *LayoutNode) SplitHServer(p *Pane, cfg *config.Config, cellW, cellH int, dir string) (*LayoutNode, *Pane, error) {
	return n.splitWith(p, HSplit, func() (*Pane, error) { return NewServer(cfg, p.Rect, cellW, cellH, dir, "") })
}

// SplitVServer is like SplitV but the new pane is backed by zurm-server (Mode B).
func (n *LayoutNode) SplitVServer(p *Pane, cfg *config.Config, cellW, cellH int, dir string) (*LayoutNode, *Pane, error) {
	return n.splitWith(p, VSplit, func() (*Pane, error) { return NewServer(cfg, p.Rect, cellW, cellH, dir, "") })
}

// replaceLeaf returns a new tree with the leaf containing p replaced by replacement.
func replaceLeaf(n *LayoutNode, p *Pane, replacement *LayoutNode) *LayoutNode {
	if n.Kind == Leaf {
		if n.Pane == p {
			return replacement
		}
		return n
	}
	n.Left = replaceLeaf(n.Left, p, replacement)
	n.Right = replaceLeaf(n.Right, p, replacement)
	return n
}

// Remove removes the leaf containing p from the tree, closes p.Term, and
// returns the new root. The sibling of p's parent replaces the parent.
// If p is the only pane, returns nil.
func (n *LayoutNode) Remove(p *Pane) *LayoutNode {
	p.Term.Close()
	result := removePane(n, p)
	if result != nil {
		result.InvalidateLeaves()
	}
	return result
}

// removePane recursively removes the leaf for p and returns the updated subtree.
func removePane(n *LayoutNode, p *Pane) *LayoutNode {
	if n.Kind == Leaf {
		if n.Pane == p {
			return nil // signal to parent: replace me with sibling
		}
		return n
	}

	// Check if one of our immediate children is the target leaf.
	if n.Left.Kind == Leaf && n.Left.Pane == p {
		return n.Right
	}
	if n.Right.Kind == Leaf && n.Right.Pane == p {
		return n.Left
	}

	// Recurse.
	newLeft := removePane(n.Left, p)
	newRight := removePane(n.Right, p)

	if newLeft == nil {
		return newRight
	}
	if newRight == nil {
		return newLeft
	}
	n.Left = newLeft
	n.Right = newRight
	return n
}

// Detach removes the leaf containing p from the tree WITHOUT closing p.Term.
// Returns the new root. If p is the only pane, returns nil.
func (n *LayoutNode) Detach(p *Pane) *LayoutNode {
	result := removePane(n, p)
	if result != nil {
		result.InvalidateLeaves()
	}
	return result
}

// AttachH inserts an existing pane as a horizontal split (left | right) beside
// the target pane. Returns the new tree root.
func (n *LayoutNode) AttachH(target, incoming *Pane) *LayoutNode {
	oldLeaf := NewLeaf(target)
	newLeaf := NewLeaf(incoming)
	split := &LayoutNode{
		Kind:  HSplit,
		Left:  oldLeaf,
		Right: newLeaf,
		Ratio: 0.5,
	}
	result := replaceLeaf(n, target, split)
	result.InvalidateLeaves()
	return result
}

// NextLeaf returns the pane after p in DFS order (wraps around).
func (n *LayoutNode) NextLeaf(p *Pane) *Pane {
	leaves := n.Leaves()
	if len(leaves) == 0 {
		return nil
	}
	for i, leaf := range leaves {
		if leaf.Pane == p {
			return leaves[(i+1)%len(leaves)].Pane
		}
	}
	return leaves[0].Pane
}

// PrevLeaf returns the pane before p in DFS order (wraps around).
func (n *LayoutNode) PrevLeaf(p *Pane) *Pane {
	leaves := n.Leaves()
	if len(leaves) == 0 {
		return nil
	}
	for i, leaf := range leaves {
		if leaf.Pane == p {
			idx := (i - 1 + len(leaves)) % len(leaves)
			return leaves[idx].Pane
		}
	}
	return leaves[0].Pane
}

// NeighborInDir returns the pane closest to p in direction (dx, dy) where
// dx/dy are -1, 0, or 1. Returns nil if no neighbor exists in that direction.
func (n *LayoutNode) NeighborInDir(p *Pane, dx, dy int) *Pane {
	leaves := n.Leaves()
	if len(leaves) <= 1 {
		return nil
	}

	// Center of the source pane.
	srcCX := float64(p.Rect.Min.X+p.Rect.Max.X) / 2
	srcCY := float64(p.Rect.Min.Y+p.Rect.Max.Y) / 2

	var best *Pane
	bestScore := math.MaxFloat64

	for _, leaf := range leaves {
		if leaf.Pane == p {
			continue
		}
		cx := float64(leaf.Pane.Rect.Min.X+leaf.Pane.Rect.Max.X) / 2
		cy := float64(leaf.Pane.Rect.Min.Y+leaf.Pane.Rect.Max.Y) / 2

		diffX := cx - srcCX
		diffY := cy - srcCY

		// Direction filter: candidate must be in the requested direction.
		if dx > 0 && diffX <= 0 {
			continue
		}
		if dx < 0 && diffX >= 0 {
			continue
		}
		if dy > 0 && diffY <= 0 {
			continue
		}
		if dy < 0 && diffY >= 0 {
			continue
		}

		dist := math.Sqrt(diffX*diffX + diffY*diffY)
		if dist < bestScore {
			bestScore = dist
			best = leaf.Pane
		}
	}

	return best
}
