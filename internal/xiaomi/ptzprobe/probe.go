// Package ptzprobe gates a single diagnostic motor step on a stable video
// baseline and measures the resulting image translation in memory.
package ptzprobe

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

var ErrVideoEnded = errors.New("ptzprobe: video ended")
var ErrFrameStale = errors.New("ptzprobe: stale video frame")
var ErrFrameTimestamp = errors.New("ptzprobe: invalid frame timestamp")
var ErrFrameTimeout = errors.New("ptzprobe: frame wait timeout")
var ErrWindowTooShort = errors.New("ptzprobe: sample window too short")
var ErrWindowTooLong = errors.New("ptzprobe: sample window too long")
var ErrSessionRead = errors.New("ptzprobe: video session read failed")
var ErrDecoderInput = errors.New("ptzprobe: decoder input failed")
var ErrDecoderOutput = errors.New("ptzprobe: decoder output failed")
var ErrDecoderPartialFrame = errors.New("ptzprobe: decoder returned a partial frame")
var ErrInvalidFrame = errors.New("ptzprobe: invalid frame")
var ErrInsufficientMotionFrames = errors.New("ptzprobe: too few transition frames")

type GrayFrame struct {
	At     time.Time
	Width  int
	Height int
	Pixels []byte
}

type StepResult interface {
	WriteSucceeded() bool
	SanitizedLine() string
}

// Video is implemented by the live single-session decoder and by offline
// simulated sources. DiagnosticMotorStep must use the same session as frames.
type Video interface {
	NextFrame(context.Context) (GrayFrame, error)
	DiagnosticMotorStep(direction string, onWrite func()) StepResult
}

type Position struct{ X, Y float64 }
type PositionReader func(context.Context) (Position, bool)

type Gates struct {
	Phase0Confirmed  bool
	AccountRestoreOK bool
}

type Config struct {
	BaselineFrames    int
	QuietFrames       int
	SampleInterval    time.Duration
	BaselineMinSpan   time.Duration
	BaselineMaxSpan   time.Duration
	MaxFrameAge       time.Duration
	FrameTimeout      time.Duration
	MotionWindow      time.Duration
	MotionInterval    time.Duration
	MotionMinFrames   int
	PatchRadius       int
	GridSpacing       int
	MaxSearch         int
	PostMoveMaxSearch int
	MinTrackedPoints  int
	CaptureFrames     bool
}

func DefaultConfig() Config {
	return Config{
		BaselineFrames: 8, QuietFrames: 8,
		SampleInterval:  500 * time.Millisecond,
		BaselineMinSpan: 3 * time.Second, BaselineMaxSpan: 5 * time.Second,
		MaxFrameAge: time.Second, FrameTimeout: 1500 * time.Millisecond,
		MotionWindow: 5 * time.Second, MotionInterval: 250 * time.Millisecond, MotionMinFrames: 8,
		PatchRadius: 5, GridSpacing: 32, MaxSearch: 16, PostMoveMaxSearch: 24, MinTrackedPoints: 6,
	}
}

type Result struct {
	Lines           []string
	FramesBefore    int
	MotionFrames    int
	FramesAfter     int
	BaselineSpan    time.Duration
	MotionSpan      time.Duration
	QuietSpan       time.Duration
	WriteAttempted  bool
	ImageStatus     string
	BaselineCapture []GrayFrame
	QuietCapture    []GrayFrame
}

func (r Result) SanitizedLines() []string { return append([]string(nil), r.Lines...) }

func Run(ctx context.Context, video Video, gates Gates, cfg Config, readPosition PositionReader) Result {
	cfg = normalizeConfig(cfg)
	result := Result{ImageStatus: "not_measured"}
	if video == nil {
		result.Lines = append(result.Lines, "PTZ_GATE status=blocked reason=video_unavailable write_attempted=no")
		return result
	}
	if !gates.Phase0Confirmed || !gates.AccountRestoreOK {
		phase0, account := "unconfirmed", "not_ok"
		if gates.Phase0Confirmed {
			phase0 = "confirmed"
		}
		if gates.AccountRestoreOK {
			account = "ok"
		}
		result.Lines = append(result.Lines, fmt.Sprintf("PTZ_GATE status=blocked phase0=%s account_restore=%s write_attempted=no", phase0, account))
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Lines = append(result.Lines, "PTZ_GATE status=blocked reason="+failureReason(err)+" write_attempted=no")
		return result
	}

	baseline, err := collectWindow(ctx, video, cfg.BaselineFrames, cfg.SampleInterval, cfg.BaselineMinSpan, cfg.BaselineMaxSpan, cfg)
	result.FramesBefore = len(baseline)
	result.BaselineSpan = framesDuration(baseline)
	if cfg.CaptureFrames {
		result.BaselineCapture = cloneFrames(baseline)
	}
	if err != nil {
		result.Lines = append(result.Lines, fmt.Sprintf("PTZ_BASELINE status=incomplete frames=%d required=%d span_ms=%d reason=%s write_attempted=no", len(baseline), cfg.BaselineFrames, result.BaselineSpan.Milliseconds(), failureReason(err)))
		return result
	}
	baseMeasurement := measureBaseline(baseline, cfg)
	result.Lines = append(result.Lines, fmt.Sprintf("PTZ_BASELINE status=%s frames=%d tracked_points=%d match_vectors=%d consensus_pct=%.1f drift_px=%.2f span_ms=%d reason=%s write_attempted=no",
		baseMeasurement.status, len(baseline), baseMeasurement.tracked, baseMeasurement.matches, baseMeasurement.consensus*100, baseMeasurement.drift, result.BaselineSpan.Milliseconds(), baseMeasurement.reason))
	if baseMeasurement.status != "stable" {
		return result
	}

	before, beforeOK := readPositionValue(ctx, readPosition)
	if err := ctx.Err(); err != nil {
		result.Lines = append(result.Lines, "PTZ_PREWRITE status=blocked stage=position_read reason="+failureReason(err)+" write_attempted=no")
		return result
	}
	result.Lines = append(result.Lines, "PTZ_POSITION phase=before status="+positionStatus(beforeOK))
	prewriteFrame, err := nextFreshFrame(ctx, video, baseline[len(baseline)-1].At, cfg)
	if err != nil {
		result.Lines = append(result.Lines, "PTZ_PREWRITE status=blocked stage=fresh_frame reason="+failureReason(err)+" write_attempted=no")
		return result
	}
	prewriteMatch := estimateTranslation(baseline[0], prewriteFrame, cfg)
	if !hasTranslationConsensus(prewriteMatch, cfg.MinTrackedPoints) || math.Hypot(prewriteMatch.dx, prewriteMatch.dy) > 2.0 {
		result.Lines = append(result.Lines, fmt.Sprintf("PTZ_PREWRITE status=blocked stage=fresh_frame reason=baseline_mismatch tracked_points=%d shift_x_px=%.2f shift_y_px=%.2f write_attempted=no",
			prewriteMatch.inliers, prewriteMatch.dx, prewriteMatch.dy))
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Lines = append(result.Lines, "PTZ_PREWRITE status=blocked stage=write_gate reason="+failureReason(err)+" write_attempted=no")
		return result
	}
	result.Lines = append(result.Lines, fmt.Sprintf("PTZ_PREWRITE status=ready frame_age_ms=%d baseline_match=stable write_attempted=no", time.Since(prewriteFrame.At).Milliseconds()))

	type captured struct {
		frames []GrayFrame
		span   time.Duration
		err    error
	}
	var motionCapture chan captured
	result.WriteAttempted = true
	step := video.DiagnosticMotorStep("left", func() {
		writeAt := time.Now()
		motionCapture = make(chan captured, 1)
		go func() {
			frames, span, captureErr := captureMotionWindow(ctx, video, writeAt, cfg)
			motionCapture <- captured{frames: frames, span: span, err: captureErr}
		}()
	})
	if step == nil {
		result.Lines = append(result.Lines, "PTZ_DIAG status=unavailable write_status=unknown reply_status=unknown motion=not_measured")
		result.Lines = append(result.Lines, "PTZ_STEP status=blocked reason=diagnostic_result_unavailable")
		return result
	}
	result.Lines = append(result.Lines, step.SanitizedLine())
	if !step.WriteSucceeded() {
		result.Lines = append(result.Lines, "PTZ_STEP status=write_error post_frames=0")
		return result
	}
	result.Lines = append(result.Lines, "PTZ_STEP status=write_ok direction=left count=1")
	if motionCapture == nil {
		result.Lines = append(result.Lines, "PTZ_MOTION_WINDOW status=not_started reason=write_callback_missing")
		return result
	}
	motion := <-motionCapture
	result.MotionFrames = len(motion.frames)
	result.MotionSpan = motion.span
	if motion.err != nil {
		result.Lines = append(result.Lines, fmt.Sprintf("PTZ_MOTION_WINDOW status=incomplete frames=%d duration_ms=%d reason=%s", len(motion.frames), motion.span.Milliseconds(), failureReason(motion.err)))
		return result
	}
	transition := measureImageShift(baseline[0], motion.frames, baseMeasurement, cfg)
	result.Lines = append(result.Lines, fmt.Sprintf("PTZ_MOTION_WINDOW status=complete frames=%d duration_ms=%d", len(motion.frames), motion.span.Milliseconds()))
	result.Lines = append(result.Lines, fmt.Sprintf("PTZ_IMAGE_TRANSITION status=%s search_px=%d frames=%d candidate_frames=%d matched_frames=%d rejected_weak_features=%d rejected_consensus=%d tracked_points=%d shift_x_px=%.2f shift_y_px=%.2f consistent_frames=%d",
		transition.status, transition.searchRadius, len(motion.frames), transition.candidateFrames, transition.matchedFrames, transition.rejectedWeakFeatures, transition.rejectedConsensus, transition.tracked, transition.dx, transition.dy, transition.consistentFrames))

	quiet, err := collectWindow(ctx, video, cfg.QuietFrames, cfg.SampleInterval, cfg.BaselineMinSpan, cfg.BaselineMaxSpan, cfg)
	result.FramesAfter = len(quiet)
	result.QuietSpan = framesDuration(quiet)
	if cfg.CaptureFrames {
		result.QuietCapture = cloneFrames(quiet)
	}
	if err != nil {
		result.Lines = append(result.Lines, fmt.Sprintf("PTZ_QUIET_WINDOW status=incomplete frames=%d required=%d span_ms=%d reason=%s", len(quiet), cfg.QuietFrames, result.QuietSpan.Milliseconds(), failureReason(err)))
		return result
	}
	quietMeasurement := measureBaseline(quiet, cfg)
	result.Lines = append(result.Lines, fmt.Sprintf("PTZ_QUIET_WINDOW status=%s frames=%d span_ms=%d drift_px=%.2f",
		quietMeasurement.status, len(quiet), result.QuietSpan.Milliseconds(), quietMeasurement.drift))
	afterPosition, afterOK := readPositionValue(ctx, readPosition)
	result.Lines = append(result.Lines, "PTZ_POSITION phase=after status="+positionStatus(afterOK)+" change="+positionChange(before, beforeOK, afterPosition, afterOK))
	image := imageMeasurement{status: "inconclusive"}
	if quietMeasurement.status == "stable" {
		image = measureImageShift(baseline[0], quiet, baseMeasurement, cfg)
	}
	result.ImageStatus = image.status
	result.Lines = append(result.Lines, fmt.Sprintf("PTZ_IMAGE_QUIET status=%s search_px=%d frames_before=%d frames_after=%d candidate_frames=%d matched_frames=%d rejected_weak_features=%d rejected_consensus=%d tracked_points=%d baseline_drift_px=%.2f median_shift_x_px=%.2f median_shift_y_px=%.2f consistent_frames=%d",
		image.status, image.searchRadius, len(baseline), len(quiet), image.candidateFrames, image.matchedFrames, image.rejectedWeakFeatures, image.rejectedConsensus, image.tracked, baseMeasurement.drift, image.dx, image.dy, image.consistentFrames))
	return result
}

func normalizeConfig(cfg Config) Config {
	d := DefaultConfig()
	if cfg == (Config{}) {
		return d
	}
	if cfg.BaselineFrames == 0 {
		cfg.BaselineFrames = d.BaselineFrames
	}
	if cfg.BaselineFrames < 8 {
		cfg.BaselineFrames = 8
	}
	if cfg.BaselineFrames > 10 {
		cfg.BaselineFrames = 10
	}
	if cfg.QuietFrames == 0 {
		cfg.QuietFrames = d.QuietFrames
	}
	if cfg.QuietFrames < 8 {
		cfg.QuietFrames = 8
	}
	if cfg.QuietFrames > 10 {
		cfg.QuietFrames = 10
	}
	if cfg.SampleInterval == 0 {
		cfg.SampleInterval = d.SampleInterval
	}
	if cfg.BaselineMinSpan == 0 {
		cfg.BaselineMinSpan = d.BaselineMinSpan
	}
	if cfg.BaselineMaxSpan == 0 {
		cfg.BaselineMaxSpan = d.BaselineMaxSpan
	}
	if cfg.BaselineMinSpan < 3*time.Second {
		cfg.BaselineMinSpan = 3 * time.Second
	}
	if cfg.BaselineMaxSpan > 5*time.Second || cfg.BaselineMaxSpan < cfg.BaselineMinSpan {
		cfg.BaselineMaxSpan = 5 * time.Second
	}
	if cfg.MaxFrameAge == 0 {
		cfg.MaxFrameAge = d.MaxFrameAge
	}
	if cfg.FrameTimeout == 0 {
		cfg.FrameTimeout = d.FrameTimeout
	}
	if cfg.MotionWindow == 0 {
		cfg.MotionWindow = d.MotionWindow
	}
	if cfg.MotionWindow < 4*time.Second {
		cfg.MotionWindow = 4 * time.Second
	}
	if cfg.MotionWindow > 6*time.Second {
		cfg.MotionWindow = 6 * time.Second
	}
	if cfg.MotionInterval == 0 {
		cfg.MotionInterval = d.MotionInterval
	}
	if cfg.MotionMinFrames == 0 {
		cfg.MotionMinFrames = d.MotionMinFrames
	}
	if cfg.PatchRadius == 0 {
		cfg.PatchRadius = d.PatchRadius
	}
	if cfg.GridSpacing == 0 {
		cfg.GridSpacing = d.GridSpacing
	}
	if cfg.MaxSearch == 0 {
		cfg.MaxSearch = d.MaxSearch
	}
	if cfg.PostMoveMaxSearch == 0 {
		cfg.PostMoveMaxSearch = d.PostMoveMaxSearch
	}
	if cfg.PostMoveMaxSearch < cfg.MaxSearch {
		cfg.PostMoveMaxSearch = cfg.MaxSearch
	}
	if cfg.PostMoveMaxSearch > 32 {
		cfg.PostMoveMaxSearch = 32
	}
	if cfg.MinTrackedPoints == 0 {
		cfg.MinTrackedPoints = d.MinTrackedPoints
	}
	return cfg
}

func collectWindow(ctx context.Context, video Video, count int, interval, minSpan, maxSpan time.Duration, cfg Config) ([]GrayFrame, error) {
	frames := make([]GrayFrame, 0, count)
	var lastAt time.Time
	for i := 0; i < count; i++ {
		if i > 0 {
			if err := waitContext(ctx, interval); err != nil {
				return frames, err
			}
		}
		frame, err := nextFreshFrame(ctx, video, lastAt, cfg)
		if err != nil {
			return frames, err
		}
		frames = append(frames, frame)
		lastAt = frame.At
	}
	span := framesDuration(frames)
	if span < minSpan {
		return frames, ErrWindowTooShort
	}
	if span > maxSpan {
		return frames, ErrWindowTooLong
	}
	return frames, nil
}

func nextFreshFrame(ctx context.Context, video Video, after time.Time, cfg Config) (GrayFrame, error) {
	frameCtx, cancel := context.WithTimeout(ctx, cfg.FrameTimeout)
	defer cancel()
	frame, err := video.NextFrame(frameCtx)
	if err != nil {
		if ctx.Err() != nil {
			return GrayFrame{}, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return GrayFrame{}, ErrFrameTimeout
		}
		return GrayFrame{}, err
	}
	if !validFrame(frame) {
		return GrayFrame{}, ErrInvalidFrame
	}
	if frame.At.IsZero() {
		return GrayFrame{}, ErrFrameTimestamp
	}
	age := time.Since(frame.At)
	if age < 0 || age > cfg.MaxFrameAge {
		return GrayFrame{}, ErrFrameStale
	}
	if !after.IsZero() && !frame.At.After(after) {
		return GrayFrame{}, ErrFrameStale
	}
	return frame, nil
}

func captureMotionWindow(ctx context.Context, video Video, writeAt time.Time, cfg Config) ([]GrayFrame, time.Duration, error) {
	started := time.Now()
	windowCtx, cancel := context.WithTimeout(ctx, cfg.MotionWindow)
	defer cancel()
	frames := make([]GrayFrame, 0, int(cfg.MotionWindow/cfg.MotionInterval)+1)
	lastAt := writeAt
	for {
		var frame GrayFrame
		var err error
		if len(frames) == 0 {
			frame, err = nextFrameAfterWrite(windowCtx, video, writeAt, cfg)
		} else {
			if err = waitContext(windowCtx, cfg.MotionInterval); err == nil {
				frame, err = nextFreshFrame(windowCtx, video, lastAt, cfg)
			}
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				break
			}
			return frames, time.Since(started), err
		}
		frames = append(frames, frame)
		lastAt = frame.At
		if time.Since(started) >= cfg.MotionWindow {
			break
		}
	}
	span := time.Since(started)
	if len(frames) < cfg.MotionMinFrames {
		return frames, span, ErrInsufficientMotionFrames
	}
	return frames, span, nil
}

func nextFrameAfterWrite(ctx context.Context, video Video, writeAt time.Time, cfg Config) (GrayFrame, error) {
	for {
		frame, err := nextFreshFrame(ctx, video, time.Time{}, cfg)
		if err != nil {
			return GrayFrame{}, err
		}
		if frame.At.After(writeAt) {
			return frame, nil
		}
		if err = waitContext(ctx, 20*time.Millisecond); err != nil {
			return GrayFrame{}, err
		}
	}
}

func waitContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func validFrame(f GrayFrame) bool {
	return f.Width > 0 && f.Height > 0 && len(f.Pixels) == f.Width*f.Height
}

func failureReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, ErrSessionRead):
		return "session_read_error"
	case errors.Is(err, ErrDecoderInput):
		return "decoder_input_error"
	case errors.Is(err, ErrDecoderPartialFrame):
		return "decoder_partial_frame"
	case errors.Is(err, ErrDecoderOutput):
		return "decoder_output_error"
	case errors.Is(err, ErrFrameStale):
		return "stale_frame"
	case errors.Is(err, ErrFrameTimestamp):
		return "invalid_frame_timestamp"
	case errors.Is(err, ErrFrameTimeout):
		return "frame_timeout"
	case errors.Is(err, ErrWindowTooShort):
		return "window_too_short"
	case errors.Is(err, ErrWindowTooLong):
		return "window_too_long"
	case errors.Is(err, ErrInvalidFrame):
		return "invalid_frame"
	case errors.Is(err, ErrInsufficientMotionFrames):
		return "insufficient_transition_frames"
	case errors.Is(err, ErrVideoEnded):
		return "video_ended"
	default:
		return "video_error"
	}
}

func readPositionValue(ctx context.Context, read PositionReader) (Position, bool) {
	if read == nil {
		return Position{}, false
	}
	return read(ctx)
}

func positionStatus(ok bool) string {
	if ok {
		return "readable"
	}
	return "unavailable"
}

func positionChange(before Position, beforeOK bool, after Position, afterOK bool) string {
	if !beforeOK || !afterOK {
		return "unavailable"
	}
	if math.Abs(after.X-before.X) > 1e-6 || math.Abs(after.Y-before.Y) > 1e-6 {
		return "changed"
	}
	return "unchanged"
}

type translation struct {
	dx, dy           float64
	tracked, inliers int
}

func hasTranslationConsensus(t translation, minPoints int) bool {
	return t.tracked >= minPoints && t.inliers >= minPoints && float64(t.inliers)/float64(max(1, t.tracked)) > 0.5
}

type baselineMeasurement struct {
	status    string
	tracked   int
	matches   int
	consensus float64
	drift     float64
	reason    string
}
type imageMeasurement struct {
	status                                  string
	dx, dy                                  float64
	tracked, consistentFrames               int
	candidateFrames, matchedFrames          int
	rejectedWeakFeatures, rejectedConsensus int
	searchRadius                            int
}

func measureBaseline(frames []GrayFrame, cfg Config) baselineMeasurement {
	if len(frames) < 2 {
		return baselineMeasurement{status: "unstable", reason: "insufficient_frames"}
	}
	var worst float64
	minTracked := int(^uint(0) >> 1)
	minMatches := int(^uint(0) >> 1)
	minConsensus := 1.0
	for _, frame := range frames[1:] {
		t := estimateTranslation(frames[0], frame, cfg)
		ratio := float64(t.inliers) / float64(max(1, t.tracked))
		minTracked = minInt(minTracked, t.inliers)
		minMatches = minInt(minMatches, t.tracked)
		minConsensus = math.Min(minConsensus, ratio)
		if t.tracked < cfg.MinTrackedPoints {
			return baselineMeasurement{status: "unstable", tracked: minTracked, matches: t.tracked, consensus: ratio, drift: worst, reason: "too_few_match_vectors"}
		}
		if t.inliers < cfg.MinTrackedPoints {
			return baselineMeasurement{status: "unstable", tracked: minTracked, matches: t.tracked, consensus: ratio, drift: worst, reason: "too_few_consistent_points"}
		}
		if !hasTranslationConsensus(t, cfg.MinTrackedPoints) {
			return baselineMeasurement{status: "unstable", tracked: minTracked, matches: t.tracked, consensus: ratio, drift: worst, reason: "low_consensus"}
		}
		drift := math.Hypot(t.dx, t.dy)
		if drift > worst {
			worst = drift
		}
	}
	if worst > 2.0 {
		return baselineMeasurement{status: "unstable", tracked: minTracked, matches: minMatches, consensus: minConsensus, drift: worst, reason: "excess_drift"}
	}
	return baselineMeasurement{status: "stable", tracked: minTracked, matches: minMatches, consensus: minConsensus, drift: worst, reason: "none"}
}

func measureImageShift(reference GrayFrame, after []GrayFrame, baseline baselineMeasurement, cfg Config) imageMeasurement {
	cfg = normalizeConfig(cfg)
	search := max(cfg.MaxSearch, cfg.PostMoveMaxSearch)
	if search > 32 {
		search = 32
	}
	imageCfg := cfg
	imageCfg.MaxSearch = search
	if len(after) == 0 {
		return imageMeasurement{status: "inconclusive", searchRadius: search}
	}
	translations := make([]translation, 0, len(after))
	minTracked := int(^uint(0) >> 1)
	consistent := 0
	weakFeatures, lowConsensus := 0, 0
	for _, frame := range after {
		t := estimateTranslation(reference, frame, imageCfg)
		if t.tracked < cfg.MinTrackedPoints || t.inliers < cfg.MinTrackedPoints {
			weakFeatures++
			continue
		}
		if !hasTranslationConsensus(t, cfg.MinTrackedPoints) {
			lowConsensus++
			continue
		}
		translations = append(translations, t)
		minTracked = minInt(minTracked, t.inliers)
	}
	if len(translations) < max(3, cfg.QuietFrames/2) {
		if minTracked == int(^uint(0)>>1) {
			minTracked = 0
		}
		return imageMeasurement{status: "inconclusive", tracked: minTracked, candidateFrames: len(after), matchedFrames: len(translations), rejectedWeakFeatures: weakFeatures, rejectedConsensus: lowConsensus, searchRadius: search}
	}
	dxs, dys := make([]float64, len(translations)), make([]float64, len(translations))
	for i, t := range translations {
		dxs[i], dys[i] = t.dx, t.dy
	}
	dx, dy := median(dxs), median(dys)
	threshold := math.Max(4.0, baseline.drift*2.0)
	for _, t := range translations {
		if math.Hypot(t.dx-dx, t.dy-dy) <= 2.5 && math.Hypot(t.dx, t.dy) > threshold {
			consistent++
		}
	}
	status := "no_consistent_shift"
	if math.Hypot(dx, dy) <= threshold {
		status = "no_consistent_shift"
	} else if consistent >= max(3, len(translations)*2/3) {
		status = "consistent_shift"
	} else {
		status = "inconclusive"
	}
	return imageMeasurement{status: status, dx: dx, dy: dy, tracked: max(0, minTracked), consistentFrames: consistent, candidateFrames: len(after), matchedFrames: len(translations), rejectedWeakFeatures: weakFeatures, rejectedConsensus: lowConsensus, searchRadius: search}
}

func estimateTranslation(reference, target GrayFrame, cfg Config) translation {
	if !validFrame(reference) || !validFrame(target) || reference.Width != target.Width || reference.Height != target.Height {
		return translation{}
	}
	w, h, radius, search := reference.Width, reference.Height, cfg.PatchRadius, cfg.MaxSearch
	type point struct{ x, y int }
	points := make([]point, 0)
	anchors := make([]point, 0)
	startX, startY := search+radius+1, search+radius+1
	endX, endY := w-search-radius-1, h-search-radius-1
	for cy := search + radius + 1; cy < h-search-radius-1; cy += cfg.GridSpacing {
		for cx := search + radius + 1; cx < w-search-radius-1; cx += cfg.GridSpacing {
			if patchVariance(reference, cx, cy, radius) >= 64 {
				points = append(points, point{x: cx, y: cy})
			}
		}
	}
	if len(points) == 0 {
		return translation{}
	}
	for cy := startY; cy < endY; cy += cfg.GridSpacing * 2 {
		for cx := startX; cx < endX; cx += cfg.GridSpacing * 2 {
			if patchVariance(reference, cx, cy, radius) >= 64 {
				anchors = append(anchors, point{x: cx, y: cy})
			}
		}
	}
	if len(anchors) < cfg.MinTrackedPoints {
		anchors = points
	}
	globalScore := func(dx, dy int) float64 {
		var total float64
		for _, p := range anchors {
			score := patchScore(reference, target, p.x, p.y, p.x+dx, p.y+dy, radius, 2)
			if score > 64 {
				score = 64
			}
			total += score
		}
		return total / float64(len(anchors))
	}
	bestScore, bestX, bestY := math.Inf(1), 0, 0
	for dy := -search; dy <= search; dy++ {
		for dx := -search; dx <= search; dx++ {
			score := globalScore(dx, dy)
			if score < bestScore {
				bestScore, bestX, bestY = score, dx, dy
			}
		}
	}

	// Validate the global shift independently in each textured patch. This
	// rejects a global candidate that only wins through unrelated local noise.
	const validationRadius = 2
	const maxPatchScore = 28.0
	type vector struct{ x, y int }
	vectors := make([]vector, 0, len(points))
	for _, p := range points {
		localScore, localX, localY := math.Inf(1), bestX, bestY
		for dy := max(-search, bestY-validationRadius); dy <= minInt(search, bestY+validationRadius); dy++ {
			for dx := max(-search, bestX-validationRadius); dx <= minInt(search, bestX+validationRadius); dx++ {
				score := patchScore(reference, target, p.x, p.y, p.x+dx, p.y+dy, radius, 1)
				if score < localScore {
					localScore, localX, localY = score, dx, dy
				}
			}
		}
		if localScore <= maxPatchScore {
			vectors = append(vectors, vector{localX, localY})
		}
	}
	if len(vectors) == 0 {
		return translation{dx: float64(bestX), dy: float64(bestY), tracked: len(points)}
	}
	allX, allY := make([]float64, len(vectors)), make([]float64, len(vectors))
	for i, v := range vectors {
		allX[i], allY[i] = float64(v.x), float64(v.y)
	}
	mdx, mdy := median(allX), median(allY)
	var inlierX, inlierY []float64
	for _, v := range vectors {
		if math.Abs(float64(v.x)-mdx) <= 1.5 && math.Abs(float64(v.y)-mdy) <= 1.5 {
			inlierX = append(inlierX, float64(v.x))
			inlierY = append(inlierY, float64(v.y))
		}
	}
	return translation{dx: median(inlierX), dy: median(inlierY), tracked: len(points), inliers: len(inlierX)}
}

func patchVariance(f GrayFrame, cx, cy, radius int) float64 {
	var sum, sum2 float64
	count := 0
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			v := float64(f.Pixels[y*f.Width+x])
			sum += v
			sum2 += v * v
			count++
		}
	}
	mean := sum / float64(count)
	return sum2/float64(count) - mean*mean
}

func patchScore(a, b GrayFrame, ax, ay, bx, by, radius, stride int) float64 {
	var meanA, meanB float64
	count := 0
	for y := -radius; y <= radius; y += stride {
		for x := -radius; x <= radius; x += stride {
			meanA += float64(a.Pixels[(ay+y)*a.Width+ax+x])
			meanB += float64(b.Pixels[(by+y)*b.Width+bx+x])
			count++
		}
	}
	meanA /= float64(count)
	meanB /= float64(count)
	var diff float64
	for y := -radius; y <= radius; y += stride {
		for x := -radius; x <= radius; x += stride {
			av := float64(a.Pixels[(ay+y)*a.Width+ax+x]) - meanA
			bv := float64(b.Pixels[(by+y)*b.Width+bx+x]) - meanB
			diff += math.Abs(av - bv)
		}
	}
	return diff / float64(count)
}

func frameSpanMillis(frames []GrayFrame) int64 {
	return framesDuration(frames).Milliseconds()
}

func framesDuration(frames []GrayFrame) time.Duration {
	if len(frames) < 2 || frames[0].At.IsZero() || frames[len(frames)-1].At.IsZero() {
		return 0
	}
	return frames[len(frames)-1].At.Sub(frames[0].At)
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
