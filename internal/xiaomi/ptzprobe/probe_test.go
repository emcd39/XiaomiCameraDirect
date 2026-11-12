package ptzprobe

import (
	"bytes"
	"context"
	"math"
	"math/rand"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeStep struct{ writeOK bool }

func (s fakeStep) WriteSucceeded() bool { return s.writeOK }
func (s fakeStep) SanitizedLine() string {
	return "PTZ_DIAG cmd_sent_id=0x112 payload={\"operation\":1} write_status=write_ok reply_status=reply_received motion=not_measured"
}

type fakeVideo struct {
	factory      func(index, writes int) GrayFrame
	frameLimit   int
	index        int
	writes       int
	direction    string
	writeAtFrame int
	stepResult   StepResult
	replyWait    time.Duration
	writeWaiting atomic.Bool
	duringWait   atomic.Int64
}

func (v *fakeVideo) NextFrame(context.Context) (GrayFrame, error) {
	if v.frameLimit > 0 && v.index >= v.frameLimit {
		return GrayFrame{}, ErrVideoEnded
	}
	if v.writeWaiting.Load() {
		v.duringWait.Add(1)
	}
	frame := v.factory(v.index, v.writes)
	frame.At = time.Now()
	v.index++
	return frame, nil
}

func (v *fakeVideo) DiagnosticMotorStep(direction string, onWrite func()) StepResult {
	v.writes++
	v.direction = direction
	v.writeAtFrame = v.index
	if v.stepResult == nil {
		v.stepResult = fakeStep{writeOK: true}
	}
	if v.stepResult.WriteSucceeded() && onWrite != nil {
		onWrite()
		if v.replyWait > 0 {
			v.writeWaiting.Store(true)
			time.Sleep(v.replyWait)
			v.writeWaiting.Store(false)
		}
	}
	return v.stepResult
}

func testConfig() Config {
	return Config{
		BaselineFrames: 8, QuietFrames: 8, SampleInterval: 450 * time.Millisecond,
		BaselineMinSpan: 3 * time.Second, BaselineMaxSpan: 5 * time.Second,
		MaxFrameAge: time.Second, FrameTimeout: 300 * time.Millisecond,
		MotionWindow: 4 * time.Second, MotionInterval: 250 * time.Millisecond, MotionMinFrames: 8,
		PatchRadius: 4, GridSpacing: 24, MaxSearch: 8, MinTrackedPoints: 6,
	}
}

func testTexture() GrayFrame {
	const width, height = 240, 180
	rng := rand.New(rand.NewSource(201))
	pixels := make([]byte, width*height)
	_, _ = rng.Read(pixels)
	return GrayFrame{Width: width, Height: height, Pixels: pixels}
}

func translated(src GrayFrame, dx, dy int) GrayFrame {
	pixels := make([]byte, len(src.Pixels))
	for y := 0; y < src.Height; y++ {
		for x := 0; x < src.Width; x++ {
			tx, ty := x+dx, y+dy
			if tx >= 0 && tx < src.Width && ty >= 0 && ty < src.Height {
				pixels[ty*src.Width+tx] = src.Pixels[y*src.Width+x]
			}
		}
	}
	return GrayFrame{Width: src.Width, Height: src.Height, Pixels: pixels}
}

func videoFactory(base, shifted GrayFrame) func(index, writes int) GrayFrame {
	return func(_ int, writes int) GrayFrame {
		if writes > 0 {
			return shifted
		}
		return base
	}
}

func TestRunDoesNotWriteWithoutPhase0OrAccountRestore(t *testing.T) {
	base := testTexture()
	for _, gates := range []Gates{{AccountRestoreOK: true}, {Phase0Confirmed: true}} {
		video := &fakeVideo{factory: videoFactory(base, base)}
		result := Run(context.Background(), video, gates, testConfig(), nil)
		if video.writes != 0 || video.index != 0 || result.WriteAttempted {
			t.Fatalf("missing gate must stop before frame read and PTZ: writes=%d frames=%d", video.writes, video.index)
		}
		if !strings.Contains(strings.Join(result.Lines, "\n"), "PTZ_GATE status=blocked") {
			t.Fatalf("missing sanitized gate status: %v", result.Lines)
		}
	}
}

func TestRunDoesNotWriteWithIncompleteBaseline(t *testing.T) {
	base := testTexture()
	video := &fakeVideo{factory: videoFactory(base, base), frameLimit: 7}
	result := Run(context.Background(), video, Gates{Phase0Confirmed: true, AccountRestoreOK: true}, testConfig(), nil)
	if video.writes != 0 || result.WriteAttempted {
		t.Fatalf("incomplete baseline must not write: writes=%d", video.writes)
	}
	if result.FramesBefore != 7 || !strings.Contains(strings.Join(result.Lines, "\n"), "PTZ_BASELINE status=incomplete frames=7 required=8") {
		t.Fatalf("unexpected incomplete baseline evidence: %+v", result)
	}
}

func TestRunDoesNotWriteWithUnstableBaseline(t *testing.T) {
	base := testTexture()
	video := &fakeVideo{factory: func(index, _ int) GrayFrame {
		if index == 0 {
			return base
		}
		return translated(base, 4, 0)
	}}
	result := Run(context.Background(), video, Gates{Phase0Confirmed: true, AccountRestoreOK: true}, testConfig(), nil)
	if video.writes != 0 || result.WriteAttempted {
		t.Fatalf("unstable baseline must not write: writes=%d", video.writes)
	}
	if !strings.Contains(strings.Join(result.Lines, "\n"), "PTZ_BASELINE status=unstable") {
		t.Fatalf("baseline should be classified unstable: %v", result.Lines)
	}
	if !strings.Contains(strings.Join(result.Lines, "\n"), "reason=excess_drift") {
		t.Fatalf("baseline diagnostic should identify excessive drift: %v", result.Lines)
	}
}

func TestTranslationConsensusRequiresStrictMajorityAndMinimumPoints(t *testing.T) {
	if !hasTranslationConsensus(translation{tracked: 23, inliers: 12}, 6) {
		t.Fatal("12 of 23 consistent vectors should pass the strict-majority gate")
	}
	if hasTranslationConsensus(translation{tracked: 23, inliers: 11}, 6) {
		t.Fatal("11 of 23 consistent vectors should fail the strict-majority gate")
	}
	if hasTranslationConsensus(translation{tracked: 5, inliers: 5}, 6) {
		t.Fatal("fewer than the minimum feature count should fail")
	}
}

func TestMeasureImageShiftReportsNoMatchFramesWithoutSentinel(t *testing.T) {
	ref := testTexture()
	flat := GrayFrame{Width: ref.Width, Height: ref.Height, Pixels: make([]byte, len(ref.Pixels)), At: time.Now()}
	result := measureImageShift(ref, []GrayFrame{flat}, baselineMeasurement{}, testConfig())
	if result.status != "inconclusive" || result.tracked != 0 || result.candidateFrames != 1 || result.matchedFrames != 0 || result.rejectedWeakFeatures != 1 {
		t.Fatalf("unexpected no-match diagnostic: %+v", result)
	}
}

func TestMeasureImageShiftStaticKnownShiftAndBeyondSearchRange(t *testing.T) {
	base := testTexture()
	frames := func(frame GrayFrame) []GrayFrame {
		out := make([]GrayFrame, 8)
		for i := range out {
			out[i] = frame
		}
		return out
	}
	cfg := testConfig()
	cfg.MaxSearch = 16
	cfg.PostMoveMaxSearch = 24
	baseline := baselineMeasurement{status: "stable", drift: 0}
	shortConfig := testConfig()
	shortFrame := translated(base, 6, -3)
	short := measureImageShift(base, frames(shortFrame), baseline, shortConfig)
	if short.status != "consistent_shift" || math.Abs(short.dx-6) > 1 || math.Abs(short.dy+3) > 1 {
		t.Fatalf("known horizontal/vertical shift result: %+v", short)
	}

	static := measureImageShift(base, frames(base), baseline, cfg)
	if static.status != "no_consistent_shift" || static.matchedFrames != 8 || math.Hypot(static.dx, static.dy) != 0 {
		t.Fatalf("static image result: %+v", static)
	}

	shifted := measureImageShift(base, frames(translated(base, -8, 0)), baseline, cfg)
	if shifted.status != "consistent_shift" || math.Abs(shifted.dx+8) > 1 || math.Abs(shifted.dy) > 1 || shifted.consistentFrames < 6 {
		t.Fatalf("known horizontal shift result: %+v", shifted)
	}

	beyond := measureImageShift(base, frames(translated(base, -20, 0)), baseline, cfg)
	if beyond.status != "consistent_shift" || beyond.searchRadius != 24 || math.Abs(beyond.dx+20) > 1 || beyond.matchedFrames != 8 {
		t.Fatalf("post-move search should detect a known -20px shift: %+v", beyond)
	}
	prewrite := make([]GrayFrame, 2)
	prewrite[0], prewrite[1] = base, translated(base, -20, 0)
	if measurement := measureBaseline(prewrite, cfg); measurement.status == "stable" {
		t.Fatalf("pre-write stability gate must keep the original ±16px search: %+v", measurement)
	}
	further := measureImageShift(base, frames(translated(base, -32, 0)), baseline, cfg)
	if further.status != "inconclusive" || further.matchedFrames >= max(3, cfg.QuietFrames/2) {
		t.Fatalf("shift beyond the widened 24px search must remain inconclusive: %+v", further)
	}
	t.Logf("static=%+v within_range=%+v widened_range=%+v beyond_widened_range=%+v", static, shifted, beyond, further)
}

func TestRunDoesNotWriteWhenDeadlineExpiresDuringPositionRead(t *testing.T) {
	base := testTexture()
	video := &fakeVideo{factory: videoFactory(base, base)}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	readPosition := func(ctx context.Context) (Position, bool) {
		<-ctx.Done()
		return Position{}, false
	}
	result := Run(ctx, video, Gates{Phase0Confirmed: true, AccountRestoreOK: true}, testConfig(), readPosition)
	if video.writes != 0 || result.WriteAttempted {
		t.Fatalf("expired position read must prevent PTZ: writes=%d", video.writes)
	}
	if !strings.Contains(strings.Join(result.Lines, "\n"), "stage=position_read reason=deadline") {
		t.Fatalf("missing sanitized position-read timeout: %v", result.Lines)
	}
}

func TestRunDoesNotWriteWhenFramesStopBeforeWrite(t *testing.T) {
	base := testTexture()
	video := &fakeVideo{factory: videoFactory(base, base), frameLimit: 8}
	readPosition := func(context.Context) (Position, bool) { return Position{}, false }
	result := Run(context.Background(), video, Gates{Phase0Confirmed: true, AccountRestoreOK: true}, testConfig(), readPosition)
	if video.writes != 0 || result.WriteAttempted {
		t.Fatalf("stopped stream must prevent PTZ: writes=%d", video.writes)
	}
	if !strings.Contains(strings.Join(result.Lines, "\n"), "stage=fresh_frame reason=video_ended") {
		t.Fatalf("missing sanitized fresh-frame failure: %v", result.Lines)
	}
}

func TestRunDoesNotWriteWhenFreshFrameNoLongerMatchesBaseline(t *testing.T) {
	base := testTexture()
	shifted := translated(base, 6, -3)
	video := &fakeVideo{factory: func(index, _ int) GrayFrame {
		if index < 8 {
			return base
		}
		return shifted
	}}
	readPosition := func(context.Context) (Position, bool) { return Position{}, false }
	result := Run(context.Background(), video, Gates{Phase0Confirmed: true, AccountRestoreOK: true}, testConfig(), readPosition)
	if video.writes != 0 || result.WriteAttempted {
		t.Fatalf("fresh but changed scene must prevent PTZ: writes=%d", video.writes)
	}
	if !strings.Contains(strings.Join(result.Lines, "\n"), "reason=baseline_mismatch") {
		t.Fatalf("missing prewrite baseline mismatch: %v", result.Lines)
	}
}

func TestRunUsesTimedWindowsAndCapturesDuringReplyWait(t *testing.T) {
	base := testTexture()
	shifted := translated(base, 6, -3)
	video := &fakeVideo{factory: videoFactory(base, shifted), replyWait: 750 * time.Millisecond}
	positions := 0
	readPosition := func(context.Context) (Position, bool) {
		positions++
		if positions == 1 {
			return Position{X: 10}, true
		}
		return Position{X: 11}, true
	}
	cfg := testConfig()
	cfg.CaptureFrames = true
	result := Run(context.Background(), video, Gates{Phase0Confirmed: true, AccountRestoreOK: true}, cfg, readPosition)
	if video.writes != 1 || video.direction != "left" || video.writeAtFrame < 9 {
		t.Fatalf("one left step should follow 8 baseline and a fresh prewrite frame: %+v", video)
	}
	if video.duringWait.Load() == 0 {
		t.Fatal("the same decoder source must be sampled while the fake 0x113 wait is pending")
	}
	if result.FramesBefore != 8 || result.MotionFrames < 8 || result.FramesAfter != 8 || video.index < 18 {
		t.Fatalf("expected baseline, transition, and quiet frames from one source: index=%d result=%+v", video.index, result)
	}
	if result.BaselineSpan < 3*time.Second || result.BaselineSpan > 5*time.Second {
		t.Fatalf("baseline frame timestamps span %s, want 3-5s", result.BaselineSpan)
	}
	if result.MotionSpan < 4*time.Second || result.MotionSpan > 6*time.Second {
		t.Fatalf("motion capture span %s, want 4-6s", result.MotionSpan)
	}
	if result.QuietSpan < 3*time.Second || result.QuietSpan > 5*time.Second {
		t.Fatalf("quiet frame timestamps span %s, want 3-5s", result.QuietSpan)
	}
	if result.ImageStatus != "consistent_shift" {
		t.Fatalf("synthetic scene shift was not measured: %+v", result.Lines)
	}
	if len(result.BaselineCapture) != 8 || len(result.QuietCapture) != 8 || !bytes.Equal(result.BaselineCapture[0].Pixels, base.Pixels) || !bytes.Equal(result.QuietCapture[0].Pixels, shifted.Pixels) || bytes.Equal(result.BaselineCapture[0].Pixels, result.QuietCapture[0].Pixels) {
		t.Fatalf("before/after frame evidence was not captured from this attempt: baseline=%d quiet=%d", len(result.BaselineCapture), len(result.QuietCapture))
	}
	joined := strings.Join(result.Lines, "\n")
	for _, want := range []string{"PTZ_PREWRITE status=ready", "PTZ_MOTION_WINDOW status=complete", "PTZ_IMAGE_TRANSITION", "PTZ_QUIET_WINDOW status=stable", "PTZ_POSITION phase=after", "PTZ_IMAGE_QUIET status=consistent_shift"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing measurement field %q in: %s", want, joined)
		}
	}
}
