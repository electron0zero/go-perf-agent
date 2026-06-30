package collect

import "testing"

func TestGcxPermanent(t *testing.T) {
	permanentOut := []string{
		"error: query exceeds 50 MB limit",
		"this response exceeds the limit",
		"unauthorized: token expired",
		"this subcommand is not yet implemented",
		"invalid argument",
	}
	for _, out := range permanentOut {
		if !gcxPermanent(out) {
			t.Errorf("gcxPermanent(%q) = false, want true (no point retrying)", out)
		}
	}
	transient := []string{"connection reset by peer", "context deadline exceeded", ""}
	for _, out := range transient {
		if gcxPermanent(out) {
			t.Errorf("gcxPermanent(%q) = true, want false (retryable)", out)
		}
	}
}
