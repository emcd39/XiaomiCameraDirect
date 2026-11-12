package ptzprobe

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func evidenceFrames(frame GrayFrame, count int, start time.Time, interval time.Duration) []GrayFrame {
	frames := make([]GrayFrame, count)
	for i := range frames {
		frames[i] = frame
		frames[i].At = start.Add(time.Duration(i) * interval)
	}
	return frames
}

func TestFrameEvidenceRoundTripReplaysWidenedShift(t *testing.T) {
	base := testTexture()
	shifted := translated(base, -20, 0)
	before := make([]GrayFrame, 8)
	after := make([]GrayFrame, 8)
	start := time.Now().UTC()
	for i := range before {
		before[i] = base
		before[i].At = start.Add(time.Duration(i) * 500 * time.Millisecond)
		after[i] = shifted
		after[i].At = start.Add(5*time.Second + time.Duration(i)*500*time.Millisecond)
	}

	root := t.TempDir()
	id, err := SaveFrameEvidence(root, before, after)
	if err != nil {
		t.Fatal(err)
	}
	loadedBefore, loadedAfter, err := LoadFrameEvidence(filepath.Join(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedBefore) != 8 || len(loadedAfter) != 8 || !bytes.Equal(loadedBefore[0].Pixels, base.Pixels) || !bytes.Equal(loadedAfter[0].Pixels, shifted.Pixels) || !loadedAfter[0].At.Equal(after[0].At) {
		t.Fatalf("evidence did not round-trip: before=%d after=%d", len(loadedBefore), len(loadedAfter))
	}
	analysis := AnalyzeEvidence(loadedBefore, loadedAfter, DefaultConfig())
	if analysis.BaselineStatus != "stable" || analysis.ImageStatus != "consistent_shift" || analysis.SearchRadius != 24 || analysis.MatchedFrames != 8 || analysis.ShiftX != -20 {
		t.Fatalf("offline replay missed the widened shift: %+v", analysis)
	}
}

func TestFrameEvidenceRejectsPathTraversal(t *testing.T) {
	base := testTexture()
	frames := []GrayFrame{base, base}
	root := t.TempDir()
	id, err := SaveFrameEvidence(root, frames, frames)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, id, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("baseline-00.pgm"), []byte("../outside.pgm"), 1)
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadFrameEvidence(filepath.Join(root, id)); err == nil {
		t.Fatal("path traversal in frame manifest was accepted")
	}
}

func TestAnalyzeEvidenceRejectsIncompleteWindows(t *testing.T) {
	base := testTexture()
	start := time.Now().UTC().Add(-10 * time.Second)
	before := evidenceFrames(base, 8, start, 500*time.Millisecond)
	shifted := translated(base, -20, 0)
	quiet := evidenceFrames(shifted, 8, start.Add(5*time.Second), 500*time.Millisecond)
	cfg := DefaultConfig()

	t.Run("short baseline", func(t *testing.T) {
		analysis := AnalyzeEvidence(before[:4], quiet, cfg)
		if analysis.BaselineStatus != "incomplete" || analysis.BaselineReason != "frame_count" || analysis.ImageStatus != "inconclusive" || analysis.MatchedFrames != 0 {
			t.Fatalf("incomplete baseline must not be measured: %+v", analysis)
		}
	})
	t.Run("short baseline duration", func(t *testing.T) {
		shortWindow := evidenceFrames(base, 8, start, 100*time.Millisecond)
		analysis := AnalyzeEvidence(shortWindow, quiet, cfg)
		if analysis.BaselineStatus != "incomplete" || analysis.BaselineReason != "window_too_short" || analysis.ImageStatus != "inconclusive" || analysis.MatchedFrames != 0 {
			t.Fatalf("short baseline duration must not be measured: %+v", analysis)
		}
	})
	t.Run("short quiet frame count", func(t *testing.T) {
		analysis := AnalyzeEvidence(before, quiet[:4], cfg)
		if analysis.BaselineStatus != "stable" || analysis.QuietStatus != "incomplete" || analysis.QuietReason != "frame_count" || analysis.ImageStatus != "inconclusive" || analysis.MatchedFrames != 0 {
			t.Fatalf("incomplete quiet frame count must not be measured: %+v", analysis)
		}
	})
	t.Run("short quiet duration", func(t *testing.T) {
		shortWindow := evidenceFrames(shifted, 8, start.Add(5*time.Second), 100*time.Millisecond)
		analysis := AnalyzeEvidence(before, shortWindow, cfg)
		if analysis.QuietStatus != "incomplete" || analysis.QuietReason != "window_too_short" || analysis.QuietSpan >= 3*time.Second || analysis.ImageStatus != "inconclusive" || analysis.MatchedFrames != 0 {
			t.Fatalf("short quiet duration must not be measured: %+v", analysis)
		}
	})
}

func TestAnalyzeEvidenceRejectsUnstableQuietWindow(t *testing.T) {
	base := testTexture()
	start := time.Now().UTC().Add(-10 * time.Second)
	before := evidenceFrames(base, 8, start, 500*time.Millisecond)
	quiet := make([]GrayFrame, 8)
	for i := range quiet {
		quiet[i] = translated(base, -20+i/2, 0)
		quiet[i].At = start.Add(5*time.Second + time.Duration(i)*500*time.Millisecond)
	}

	analysis := AnalyzeEvidence(before, quiet, DefaultConfig())
	if analysis.BaselineStatus != "stable" || analysis.QuietStatus != "unstable" || analysis.QuietReason == "none" || analysis.QuietDriftPx <= 2 || analysis.ImageStatus != "inconclusive" || analysis.MatchedFrames != 0 {
		t.Fatalf("unstable quiet window must not be reported as image motion: %+v", analysis)
	}
}
