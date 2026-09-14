package ownership

import "testing"

func TestNamesRejectTraversal(t *testing.T) {
	if _, _, err := Names("../root"); err == nil {
		t.Fatal("traversal accepted")
	}
}
func TestNamesDeterministic(t *testing.T) {
	a, b, _ := Names("inst-123")
	c, d, _ := Names("inst-123")
	if a != c || b != d {
		t.Fatal("names not deterministic")
	}
}
