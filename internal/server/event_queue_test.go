package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildEventRowsGroupsAllMatchesForQueue(t *testing.T) {
	h := &Handler{
		edgeState: make(map[string]bool),
	}

	rows := h.buildEventRows("bbox", "cam1", 123, map[string]float64{"person": 1}, []EventRule{
		{
			ID:         "match",
			Name:       "Match",
			Expression: "person > 0",
			RecordMode: "ALL_MATCHES",
			Enabled:    true,
		},
		{
			ID:         "edge",
			Name:       "Edge",
			Expression: "person > 0",
			RecordMode: "EDGE_ONLY",
			Enabled:    true,
		},
	})

	require.Equal(t, 2, rows.len())
	require.Len(t, rows.allMatches, 1)
	require.Len(t, rows.edgeOrOther, 1)
	assert.Equal(t, "cam1.match", rows.allMatches[0].Name)
	assert.Equal(t, "cam1.edge", rows.edgeOrOther[0].Name)
}

func TestNextRetryDelayCapsAtMax(t *testing.T) {
	assert.Equal(t, 2*time.Second, nextRetryDelay(time.Second, 5*time.Second))
	assert.Equal(t, 5*time.Second, nextRetryDelay(5*time.Second, 5*time.Second))
	assert.Equal(t, 5*time.Second, nextRetryDelay(4*time.Second, 5*time.Second))
}

func TestVideoEventQueueForCameraUsesIndependentQueues(t *testing.T) {
	h := &Handler{}
	h.startEventQueue()
	t.Cleanup(func() {
		if h.eventQueueCancel != nil {
			h.eventQueueCancel()
		}
	})

	cam1Queue := h.videoEventQueueForCamera("cam1")
	cam1QueueAgain := h.videoEventQueueForCamera("cam1")
	cam2Queue := h.videoEventQueueForCamera("cam2")

	require.NotNil(t, cam1Queue)
	require.True(t, cam1Queue == cam1QueueAgain)
	require.True(t, cam1Queue != cam2Queue)
	require.Len(t, h.videoEventQueues, 2)
}
