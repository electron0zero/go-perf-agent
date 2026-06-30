package collect

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGcxPermanent(t *testing.T) {
	for _, out := range []string{
		"error: query exceeds 50 MB limit",
		"this response exceeds the limit",
		"unauthorized: token expired",
		"this subcommand is not yet implemented",
		"invalid argument",
	} {
		require.True(t, gcxPermanent(out), "permanent (no point retrying): %q", out)
	}
	for _, out := range []string{"connection reset by peer", "context deadline exceeded", ""} {
		require.False(t, gcxPermanent(out), "retryable: %q", out)
	}
}
