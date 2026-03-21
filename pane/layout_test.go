package pane

import (
	"image"
	"testing"
)

// testPane creates a minimal Pane for layout testing (no Terminal).
func testPane(name string) *Pane {
	return &Pane{CustomName: name}
}

func TestNewLeaf(t *testing.T) {
	p := testPane("A")
	n := NewLeaf(p)
	if n.Kind != Leaf {
		t.Errorf("kind = %d, want Leaf", n.Kind)
	}
	if n.Pane != p {
		t.Error("pane mismatch")
	}
	if n.Ratio != 0.5 {
		t.Errorf("ratio = %f, want 0.5", n.Ratio)
	}
}

func TestLeaves_SingleLeaf(t *testing.T) {
	n := NewLeaf(testPane("A"))
	leaves := n.Leaves()
	if len(leaves) != 1 {
		t.Fatalf("len(leaves) = %d, want 1", len(leaves))
	}
	if leaves[0].Pane.CustomName != "A" {
		t.Errorf("leaf name = %q, want %q", leaves[0].Pane.CustomName, "A")
	}
}

func TestLeaves_HSplit(t *testing.T) {
	a := testPane("A")
	b := testPane("B")
	root := &LayoutNode{
		Kind:  HSplit,
		Left:  NewLeaf(a),
		Right: NewLeaf(b),
		Ratio: 0.5,
	}
	leaves := root.Leaves()
	if len(leaves) != 2 {
		t.Fatalf("len(leaves) = %d, want 2", len(leaves))
	}
	if leaves[0].Pane != a || leaves[1].Pane != b {
		t.Error("leaves order: expected A, B")
	}
}

func TestLeaves_Nested(t *testing.T) {
	a, b, c := testPane("A"), testPane("B"), testPane("C")
	//       HSplit
	//      /      \
	//     A     VSplit
	//          /      \
	//         B        C
	root := &LayoutNode{
		Kind: HSplit,
		Left: NewLeaf(a),
		Right: &LayoutNode{
			Kind:  VSplit,
			Left:  NewLeaf(b),
			Right: NewLeaf(c),
			Ratio: 0.5,
		},
		Ratio: 0.5,
	}
	leaves := root.Leaves()
	if len(leaves) != 3 {
		t.Fatalf("len(leaves) = %d, want 3", len(leaves))
	}
	names := []string{leaves[0].Pane.CustomName, leaves[1].Pane.CustomName, leaves[2].Pane.CustomName}
	if names[0] != "A" || names[1] != "B" || names[2] != "C" {
		t.Errorf("leaves = %v, want [A B C]", names)
	}
}

func TestLeaves_CacheInvalidation(t *testing.T) {
	a, b := testPane("A"), testPane("B")
	root := &LayoutNode{
		Kind:  HSplit,
		Left:  NewLeaf(a),
		Right: NewLeaf(b),
		Ratio: 0.5,
	}
	// First call caches.
	l1 := root.Leaves()
	if len(l1) != 2 {
		t.Fatalf("first call: %d leaves", len(l1))
	}
	// Same pointer returned (cached).
	l2 := root.Leaves()
	if &l1[0] != &l2[0] {
		t.Error("expected cached slice")
	}
	// Invalidate and re-query.
	root.InvalidateLeaves()
	l3 := root.Leaves()
	if len(l3) != 2 {
		t.Fatalf("after invalidate: %d leaves", len(l3))
	}
}

func TestComputeRects_SingleLeaf(t *testing.T) {
	p := testPane("A")
	n := NewLeaf(p)
	rect := image.Rect(0, 0, 800, 600)
	n.ComputeRects(rect, 10, 20, 4, 1)
	if n.Rect != rect {
		t.Errorf("node rect = %v, want %v", n.Rect, rect)
	}
	if p.Rect != rect {
		t.Errorf("pane rect = %v, want %v", p.Rect, rect)
	}
	// Cols = (800 - 2*4) / 10 = 79.2 → 79
	if p.Cols != 79 {
		t.Errorf("cols = %d, want 79", p.Cols)
	}
	// Rows = (600 - 4 - 0) / 20 = 29.8 → 29
	if p.Rows != 29 {
		t.Errorf("rows = %d, want 29", p.Rows)
	}
}

func TestComputeRects_HSplit(t *testing.T) {
	a, b := testPane("A"), testPane("B")
	root := &LayoutNode{
		Kind:  HSplit,
		Left:  NewLeaf(a),
		Right: NewLeaf(b),
		Ratio: 0.5,
	}
	root.ComputeRects(image.Rect(0, 0, 800, 600), 10, 20, 4, 1)

	// Left gets first half, right gets second half (minus divider).
	if a.Rect.Min.X != 0 {
		t.Errorf("A min.X = %d, want 0", a.Rect.Min.X)
	}
	if b.Rect.Min.X <= a.Rect.Max.X {
		t.Errorf("B should start after A: B.min.X=%d, A.max.X=%d", b.Rect.Min.X, a.Rect.Max.X)
	}
	if b.Rect.Max.X != 800 {
		t.Errorf("B max.X = %d, want 800", b.Rect.Max.X)
	}
}

func TestComputeRects_VSplit(t *testing.T) {
	a, b := testPane("A"), testPane("B")
	root := &LayoutNode{
		Kind:  VSplit,
		Left:  NewLeaf(a),
		Right: NewLeaf(b),
		Ratio: 0.5,
	}
	root.ComputeRects(image.Rect(0, 0, 800, 600), 10, 20, 4, 1)

	if a.Rect.Min.Y != 0 {
		t.Errorf("A min.Y = %d, want 0", a.Rect.Min.Y)
	}
	if b.Rect.Min.Y <= a.Rect.Max.Y {
		t.Errorf("B should start after A: B.min.Y=%d, A.max.Y=%d", b.Rect.Min.Y, a.Rect.Max.Y)
	}
	if b.Rect.Max.Y != 600 {
		t.Errorf("B max.Y = %d, want 600", b.Rect.Max.Y)
	}
}

func TestComputeRects_MinimumSize(t *testing.T) {
	p := testPane("A")
	n := NewLeaf(p)
	// Very small rect — cols and rows should clamp to 1.
	n.ComputeRects(image.Rect(0, 0, 5, 5), 10, 20, 4, 1)
	if p.Cols < 1 {
		t.Errorf("cols = %d, should be >= 1", p.Cols)
	}
	if p.Rows < 1 {
		t.Errorf("rows = %d, should be >= 1", p.Rows)
	}
}

func TestPaneAt(t *testing.T) {
	a, b := testPane("A"), testPane("B")
	root := &LayoutNode{
		Kind:  HSplit,
		Left:  NewLeaf(a),
		Right: NewLeaf(b),
		Ratio: 0.5,
	}
	root.ComputeRects(image.Rect(0, 0, 800, 600), 10, 20, 4, 1)

	if got := root.PaneAt(100, 300); got != a {
		t.Error("expected pane A at (100,300)")
	}
	if got := root.PaneAt(600, 300); got != b {
		t.Error("expected pane B at (600,300)")
	}
	if got := root.PaneAt(9999, 9999); got != nil {
		t.Error("expected nil for out-of-bounds")
	}
}

func TestFindParent(t *testing.T) {
	a, b, c := testPane("A"), testPane("B"), testPane("C")
	rightSplit := &LayoutNode{
		Kind:  VSplit,
		Left:  NewLeaf(b),
		Right: NewLeaf(c),
		Ratio: 0.5,
	}
	root := &LayoutNode{
		Kind:  HSplit,
		Left:  NewLeaf(a),
		Right: rightSplit,
		Ratio: 0.5,
	}

	// Parent of A should be root, isLeft=true.
	parent, isLeft := root.FindParent(a)
	if parent != root || !isLeft {
		t.Errorf("FindParent(A): parent=%v, isLeft=%v", parent, isLeft)
	}

	// Parent of B should be rightSplit, isLeft=true.
	parent, isLeft = root.FindParent(b)
	if parent != rightSplit || !isLeft {
		t.Errorf("FindParent(B): parent=%v, isLeft=%v", parent, isLeft)
	}

	// Parent of C should be rightSplit, isLeft=false.
	parent, isLeft = root.FindParent(c)
	if parent != rightSplit || isLeft {
		t.Errorf("FindParent(C): parent=%v, isLeft=%v", parent, isLeft)
	}
}

func TestNextLeaf(t *testing.T) {
	a, b, c := testPane("A"), testPane("B"), testPane("C")
	root := &LayoutNode{
		Kind: HSplit,
		Left: NewLeaf(a),
		Right: &LayoutNode{
			Kind:  VSplit,
			Left:  NewLeaf(b),
			Right: NewLeaf(c),
			Ratio: 0.5,
		},
		Ratio: 0.5,
	}

	if got := root.NextLeaf(a); got != b {
		t.Errorf("NextLeaf(A) = %q, want B", got.CustomName)
	}
	if got := root.NextLeaf(b); got != c {
		t.Errorf("NextLeaf(B) = %q, want C", got.CustomName)
	}
	// Wrap around.
	if got := root.NextLeaf(c); got != a {
		t.Errorf("NextLeaf(C) = %q, want A (wrap)", got.CustomName)
	}
}

func TestPrevLeaf(t *testing.T) {
	a, b, c := testPane("A"), testPane("B"), testPane("C")
	root := &LayoutNode{
		Kind: HSplit,
		Left: NewLeaf(a),
		Right: &LayoutNode{
			Kind:  VSplit,
			Left:  NewLeaf(b),
			Right: NewLeaf(c),
			Ratio: 0.5,
		},
		Ratio: 0.5,
	}

	if got := root.PrevLeaf(c); got != b {
		t.Errorf("PrevLeaf(C) = %q, want B", got.CustomName)
	}
	if got := root.PrevLeaf(b); got != a {
		t.Errorf("PrevLeaf(B) = %q, want A", got.CustomName)
	}
	// Wrap around.
	if got := root.PrevLeaf(a); got != c {
		t.Errorf("PrevLeaf(A) = %q, want C (wrap)", got.CustomName)
	}
}

func TestNeighborInDir(t *testing.T) {
	a, b := testPane("A"), testPane("B")
	root := &LayoutNode{
		Kind:  HSplit,
		Left:  NewLeaf(a),
		Right: NewLeaf(b),
		Ratio: 0.5,
	}
	root.ComputeRects(image.Rect(0, 0, 800, 600), 10, 20, 4, 1)

	// A is left, B is right.
	if got := root.NeighborInDir(a, 1, 0); got != b {
		t.Error("neighbor right of A should be B")
	}
	if got := root.NeighborInDir(b, -1, 0); got != a {
		t.Error("neighbor left of B should be A")
	}
	if got := root.NeighborInDir(a, -1, 0); got != nil {
		t.Error("no neighbor left of A")
	}
}

func TestSplitAt(t *testing.T) {
	a, b := testPane("A"), testPane("B")
	root := &LayoutNode{
		Kind:  HSplit,
		Left:  NewLeaf(a),
		Right: NewLeaf(b),
		Ratio: 0.5,
	}
	root.ComputeRects(image.Rect(0, 0, 800, 600), 10, 20, 4, 1)

	// Hit the divider between A and B.
	divX := root.Left.Rect.Max.X
	if got := root.SplitAt(divX, 300, 3); got != root {
		t.Error("expected to find root split at divider")
	}
	// Miss — far from divider.
	if got := root.SplitAt(100, 300, 3); got != nil {
		t.Error("expected nil away from divider")
	}
}

func TestReplaceLeaf(t *testing.T) {
	a, b := testPane("A"), testPane("B")
	root := NewLeaf(a)

	replacement := NewLeaf(b)
	result := replaceLeaf(root, a, replacement)
	if result.Pane != b {
		t.Error("expected root replaced with B")
	}
}

func TestDetach(t *testing.T) {
	a, b, c := testPane("A"), testPane("B"), testPane("C")
	root := &LayoutNode{
		Kind: HSplit,
		Left: NewLeaf(a),
		Right: &LayoutNode{
			Kind:  VSplit,
			Left:  NewLeaf(b),
			Right: NewLeaf(c),
			Ratio: 0.5,
		},
		Ratio: 0.5,
	}

	// Detach B — sibling C should replace the VSplit.
	result := root.Detach(b)
	leaves := result.Leaves()
	if len(leaves) != 2 {
		t.Fatalf("after detach B: %d leaves, want 2", len(leaves))
	}
	names := []string{leaves[0].Pane.CustomName, leaves[1].Pane.CustomName}
	if names[0] != "A" || names[1] != "C" {
		t.Errorf("leaves = %v, want [A C]", names)
	}
}

func TestDetach_LastPane(t *testing.T) {
	a := testPane("A")
	root := NewLeaf(a)
	result := root.Detach(a)
	if result != nil {
		t.Error("detaching last pane should return nil")
	}
}

func TestAttachH(t *testing.T) {
	a, b := testPane("A"), testPane("B")
	root := NewLeaf(a)

	result := root.AttachH(a, b)
	if result.Kind != HSplit {
		t.Errorf("kind = %d, want HSplit", result.Kind)
	}
	leaves := result.Leaves()
	if len(leaves) != 2 {
		t.Fatalf("len(leaves) = %d, want 2", len(leaves))
	}
	if leaves[0].Pane != a || leaves[1].Pane != b {
		t.Error("expected [A, B]")
	}
}
