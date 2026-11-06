package checks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The recent window of promql/series must start on its step, otherwise it can share a range
// query cache key with a slice of the lookback range that starts a few seconds earlier and
// make that slice lose its first sample.
func TestStepAlignedRange(t *testing.T) {
	for _, step := range []time.Duration{time.Minute * 5, time.Minute * 10} {
		before := time.Now()
		r := newStepAlignedRange(time.Hour, step)
		require.Equal(t, r.Start(), r.Start().Truncate(step), "start isn't a multiple of the step")
		require.False(t, r.Start().After(before.Add(-time.Hour)), "start is later than the lookback")
		require.Less(t, r.RelativeRange.Start().Sub(r.Start()), step, "start moved back more than one step")
		require.Equal(t, time.Hour, r.Dur())
		require.Equal(t, step, r.Step())
	}
}
