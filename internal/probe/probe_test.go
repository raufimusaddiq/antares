package probe

import "testing"

func TestProbe(t *testing.T) {
	if Probe() != "ok" {
		t.Fatal("unexpected")
	}
}
