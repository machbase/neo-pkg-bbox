package server

import (
	"context"
	"errors"
	"time"

	"github.com/machbase/neo-pkg-bbox/internal/db"
	"github.com/machbase/neo-pkg-bbox/internal/logger"
)

const videoEventQueueSize = 16384
const videoQueueFullLogInterval = 30 * time.Second
const videoGapDropGrace = 2 * time.Second

const (
	eventQueueModeAllMatches = "ALL_MATCHES"
	eventQueueModeEdgeOnly   = "EDGE_ONLY"
)

type queuedVideoEvent struct {
	camera       CameraCreateRequest
	tsNano       int64
	events       []db.CameraEventRow
	mode         string
	enqueuedAt   time.Time
	nextAttempt  time.Time
	nextDelay    time.Duration
	attempts     int
	lastCheckErr error
}

type queueFullLogState struct {
	lastLogged time.Time
	suppressed int
}

func (h *Handler) startEventQueue() {
	if h.eventQueueCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.eventQueueCtx = ctx
	h.eventQueueCancel = cancel
}

func (h *Handler) enqueueVideoEvents(camera CameraCreateRequest, tsNano int64, events []db.CameraEventRow, mode string) {
	if len(events) == 0 {
		return
	}
	queue := h.videoEventQueueForCamera(camera.Name)
	if queue == nil {
		go h.persistEventsWhenVideoReady(camera, tsNano, events)
		return
	}

	now := time.Now()
	item := queuedVideoEvent{
		camera:      camera,
		tsNano:      tsNano,
		events:      append([]db.CameraEventRow(nil), events...),
		mode:        mode,
		enqueuedAt:  now,
		nextAttempt: now,
		nextDelay:   h.eventCfg.VideoRetryInitialDuration(),
	}

	select {
	case queue <- item:
	default:
		h.logVideoQueueFullDrop(camera, tsNano, len(events), mode)
	}
}

func (h *Handler) videoEventQueueForCamera(cameraID string) chan queuedVideoEvent {
	ctx := h.eventQueueCtx
	if ctx == nil || h.eventQueueCancel == nil {
		return nil
	}
	if cameraID == "" {
		return nil
	}

	h.videoEventQueueMu.Lock()
	defer h.videoEventQueueMu.Unlock()

	if h.videoEventQueues == nil {
		h.videoEventQueues = make(map[string]chan queuedVideoEvent)
	}
	if queue := h.videoEventQueues[cameraID]; queue != nil {
		return queue
	}

	queue := make(chan queuedVideoEvent, videoEventQueueSize)
	h.videoEventQueues[cameraID] = queue
	go h.runVideoEventQueue(ctx, cameraID, queue)
	return queue
}

func (h *Handler) runVideoEventQueue(ctx context.Context, cameraID string, queue chan queuedVideoEvent) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var pending []queuedVideoEvent
	for {
		select {
		case <-ctx.Done():
			if len(pending) > 0 {
				logger.GetLogger().Warnf("[event] video event queue stopped camera=%s pending=%d", cameraID, len(pending))
			}
			return
		case item := <-queue:
			pending = append(pending, item)
			pending = h.drainVideoEventQueue(pending, queue)
			pending = h.processVideoEventQueue(ctx, pending, time.Now())
		case now := <-ticker.C:
			pending = h.drainVideoEventQueue(pending, queue)
			pending = h.processVideoEventQueue(ctx, pending, now)
		}
	}
}

func (h *Handler) drainVideoEventQueue(pending []queuedVideoEvent, queue chan queuedVideoEvent) []queuedVideoEvent {
	for {
		select {
		case item := <-queue:
			pending = append(pending, item)
		default:
			return pending
		}
	}
}

func (h *Handler) processVideoEventQueue(ctx context.Context, pending []queuedVideoEvent, now time.Time) []queuedVideoEvent {
	if len(pending) == 0 {
		return pending
	}

	keep := pending[:0]
	waitDuration := h.eventCfg.VideoWaitDuration()
	maxDelay := h.eventCfg.VideoRetryMaxDuration()

	for _, item := range pending {
		if now.Before(item.nextAttempt) {
			keep = append(keep, item)
			continue
		}

		deadline := item.enqueuedAt.Add(waitDuration)
		if now.After(deadline) {
			h.logQueuedEventDrop(item, waitDuration)
			continue
		}

		item.attempts++
		ok, err := h.videoChunkAvailable(ctx, item.camera, item.tsNano)
		if ok {
			if err := h.insertCameraEvents(ctx, item.camera.Table, item.camera.Name, item.events); err != nil {
				logger.GetLogger().Warnf("[event] failed camera=%s table=%s timestamp_ns=%d events=%d reason=insert_failed mode=%s error=%v",
					item.camera.Name, item.camera.Table, item.tsNano, len(item.events), item.mode, err)
			} else {
				logger.GetLogger().Infof("[event] saved camera=%s table=%s timestamp_ns=%d events=%d attempts=%d video_required=true mode=%s",
					item.camera.Name, item.camera.Table, item.tsNano, len(item.events), item.attempts, item.mode)
			}
			continue
		}

		item.lastCheckErr = err
		if err == nil && now.Sub(item.enqueuedAt) >= videoGapDropGrace {
			nextOK, nextErr := h.nextVideoChunkAvailable(ctx, item.camera, item.tsNano)
			if nextErr != nil {
				item.lastCheckErr = nextErr
			} else if nextOK {
				h.logQueuedEventGapDrop(item)
				continue
			}
		}
		if !deadline.After(now) {
			h.logQueuedEventDrop(item, waitDuration)
			continue
		}

		delay := item.nextDelay
		if delay <= 0 {
			delay = h.eventCfg.VideoRetryInitialDuration()
		}
		nextAttempt := now.Add(delay)
		if nextAttempt.After(deadline) {
			nextAttempt = deadline
		}
		item.nextAttempt = nextAttempt
		item.nextDelay = nextRetryDelay(delay, maxDelay)
		keep = append(keep, item)
	}

	for i := len(keep); i < len(pending); i++ {
		pending[i] = queuedVideoEvent{}
	}
	return keep
}

func nextRetryDelay(current, max time.Duration) time.Duration {
	next := current + time.Second
	if max > 0 && next > max {
		return max
	}
	return next
}

func (h *Handler) logQueuedEventDrop(item queuedVideoEvent, waitDuration time.Duration) {
	if item.lastCheckErr != nil && !errors.Is(item.lastCheckErr, context.Canceled) && !errors.Is(item.lastCheckErr, context.DeadlineExceeded) {
		logger.GetLogger().Warnf("[event] dropped camera=%s table=%s timestamp_ns=%d events=%d attempts=%d reason=video_check_failed mode=%s error=%v",
			item.camera.Name, item.camera.Table, item.tsNano, len(item.events), item.attempts, item.mode, item.lastCheckErr)
		return
	}
	logger.GetLogger().Warnf("[event] dropped camera=%s table=%s timestamp_ns=%d events=%d attempts=%d reason=video_not_available mode=%s wait_seconds=%d",
		item.camera.Name, item.camera.Table, item.tsNano, len(item.events), item.attempts, item.mode, int(waitDuration/time.Second))
}

func (h *Handler) logQueuedEventGapDrop(item queuedVideoEvent) {
	logger.GetLogger().Warnf("[event] dropped camera=%s table=%s timestamp_ns=%d events=%d attempts=%d reason=video_gap_after_timestamp mode=%s",
		item.camera.Name, item.camera.Table, item.tsNano, len(item.events), item.attempts, item.mode)
}

func (h *Handler) logVideoQueueFullDrop(camera CameraCreateRequest, tsNano int64, eventCount int, mode string) {
	now := time.Now()
	key := camera.Name + "\x00" + mode

	h.videoQueueDropMu.Lock()
	if h.videoQueueDropStats == nil {
		h.videoQueueDropStats = make(map[string]queueFullLogState)
	}
	state := h.videoQueueDropStats[key]
	state.suppressed++
	shouldLog := state.lastLogged.IsZero() || now.Sub(state.lastLogged) >= videoQueueFullLogInterval
	dropped := state.suppressed
	if shouldLog {
		state.lastLogged = now
		state.suppressed = 0
	}
	h.videoQueueDropStats[key] = state
	h.videoQueueDropMu.Unlock()

	if !shouldLog {
		return
	}

	logger.GetLogger().Warnf("[event] dropped camera=%s table=%s timestamp_ns=%d events=%d reason=camera_video_event_queue_full mode=%s queue_capacity=%d dropped_since_last_log=%d",
		camera.Name, camera.Table, tsNano, eventCount, mode, videoEventQueueSize, dropped)
}
