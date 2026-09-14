package reconciliation

import "testing"

func TestClassify(t *testing.T) {
	if Classify("x", "x", true) != Healthy || Classify("x", "y", true) != SecurityDrift || Classify("x", "x", false) != Conflict {
		t.Fatal("classification")
	}
}
