package ptzprobe

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	evidenceFormat       = "xlg-ptz-gray-evidence-v1"
	maxEvidenceFrames    = 10
	maxEvidenceFrameSize = 4 << 20
)

type evidenceFrame struct {
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

type evidenceManifest struct {
	Format     string          `json:"format"`
	CapturedAt time.Time       `json:"captured_at"`
	Baseline   []evidenceFrame `json:"baseline"`
	Quiet      []evidenceFrame `json:"quiet"`
}

// EvidenceAnalysis is a sanitized summary suitable for console output or an
// offline replay report.
type EvidenceAnalysis struct {
	BaselineStatus       string
	BaselineReason       string
	BaselineFrames       int
	BaselineSpan         time.Duration
	BaselinePoints       int
	BaselineMatchVectors int
	BaselineConsensusPct float64
	BaselineDriftPx      float64
	QuietStatus          string
	QuietReason          string
	QuietSpan            time.Duration
	QuietDriftPx         float64
	ImageStatus          string
	SearchRadius         int
	CandidateFrames      int
	MatchedFrames        int
	RejectedWeakFeatures int
	RejectedConsensus    int
	TrackedPoints        int
	ShiftX               float64
	ShiftY               float64
	ConsistentFrames     int
	AfterFrames          int
}

// AnalyzeEvidence replays a saved before/after capture with the same complete
// window, baseline stability, quiet-window stability, and post-move image gates
// used by a live probe.
func AnalyzeEvidence(before, after []GrayFrame, cfg Config) EvidenceAnalysis {
	cfg = normalizeConfig(cfg)
	baselineWindowStatus, baselineWindowReason, baselineSpan := validateEvidenceWindow(before, cfg.BaselineFrames, cfg.BaselineMinSpan, cfg.BaselineMaxSpan)
	analysis := EvidenceAnalysis{
		BaselineStatus: baselineWindowStatus,
		BaselineReason: baselineWindowReason,
		BaselineFrames: len(before),
		BaselineSpan:   baselineSpan,
		QuietStatus:    "not_checked",
		ImageStatus:    "inconclusive",
		AfterFrames:    len(after),
	}
	if baselineWindowStatus != "complete" {
		return analysis
	}
	baseline := measureBaseline(before, cfg)
	analysis.BaselineStatus = baseline.status
	analysis.BaselineReason = baseline.reason
	analysis.BaselinePoints = baseline.tracked
	analysis.BaselineMatchVectors = baseline.matches
	analysis.BaselineConsensusPct = baseline.consensus * 100
	analysis.BaselineDriftPx = baseline.drift
	if baseline.status != "stable" {
		return analysis
	}
	quietWindowStatus, quietWindowReason, quietSpan := validateEvidenceWindow(after, cfg.QuietFrames, cfg.BaselineMinSpan, cfg.BaselineMaxSpan)
	analysis.QuietStatus = quietWindowStatus
	analysis.QuietReason = quietWindowReason
	analysis.QuietSpan = quietSpan
	if quietWindowStatus != "complete" {
		return analysis
	}
	quiet := measureBaseline(after, cfg)
	analysis.QuietStatus = quiet.status
	analysis.QuietReason = quiet.reason
	analysis.QuietDriftPx = quiet.drift
	if quiet.status != "stable" {
		return analysis
	}
	image := measureImageShift(before[0], after, baseline, cfg)
	analysis.ImageStatus = image.status
	analysis.SearchRadius = image.searchRadius
	analysis.CandidateFrames = image.candidateFrames
	analysis.MatchedFrames = image.matchedFrames
	analysis.RejectedWeakFeatures = image.rejectedWeakFeatures
	analysis.RejectedConsensus = image.rejectedConsensus
	analysis.TrackedPoints = image.tracked
	analysis.ShiftX = image.dx
	analysis.ShiftY = image.dy
	analysis.ConsistentFrames = image.consistentFrames
	return analysis
}

func validateEvidenceWindow(frames []GrayFrame, required int, minSpan, maxSpan time.Duration) (status, reason string, span time.Duration) {
	span = framesDuration(frames)
	if len(frames) != required {
		return "incomplete", "frame_count", span
	}
	for i := range frames {
		if frames[i].At.IsZero() || i > 0 && !frames[i].At.After(frames[i-1].At) {
			return "incomplete", "invalid_timestamps", span
		}
	}
	if span < minSpan {
		return "incomplete", "window_too_short", span
	}
	if span > maxSpan {
		return "incomplete", "window_too_long", span
	}
	return "complete", "none", span
}

// SaveFrameEvidence stores grayscale before/after frames in private PGM files
// plus timestamps in a manifest. It returns only the generated directory ID.
func SaveFrameEvidence(root string, baseline, quiet []GrayFrame) (string, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return "", errors.New("evidence root must be absolute")
	}
	if len(baseline) > maxEvidenceFrames || len(quiet) > maxEvidenceFrames {
		return "", errors.New("evidence frame count exceeds limit")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", errors.New("evidence directory unavailable")
	}
	dir, err := os.MkdirTemp(root, "ptz-capture-")
	if err != nil {
		return "", errors.New("evidence capture directory unavailable")
	}
	manifest := evidenceManifest{Format: evidenceFormat, CapturedAt: time.Now().UTC()}
	writeFrames := func(prefix string, frames []GrayFrame) ([]evidenceFrame, error) {
		refs := make([]evidenceFrame, 0, len(frames))
		for i, frame := range frames {
			if !validFrame(frame) || len(frame.Pixels) > maxEvidenceFrameSize {
				return nil, errors.New("invalid evidence frame")
			}
			name := fmt.Sprintf("%s-%02d.pgm", prefix, i)
			path := filepath.Join(dir, name)
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return nil, errors.New("evidence frame write failed")
			}
			_, headerErr := fmt.Fprintf(file, "P5\n%d %d\n255\n", frame.Width, frame.Height)
			var dataErr error
			if headerErr == nil {
				_, dataErr = file.Write(frame.Pixels)
			}
			closeErr := file.Close()
			if headerErr != nil || dataErr != nil || closeErr != nil {
				return nil, errors.New("evidence frame write failed")
			}
			refs = append(refs, evidenceFrame{Name: name, At: frame.At.UTC()})
		}
		return refs, nil
	}
	manifest.Baseline, err = writeFrames("baseline", baseline)
	if err != nil {
		return filepath.Base(dir), err
	}
	manifest.Quiet, err = writeFrames("quiet", quiet)
	if err != nil {
		return filepath.Base(dir), err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return filepath.Base(dir), errors.New("evidence manifest encoding failed")
	}
	path := filepath.Join(dir, "manifest.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return filepath.Base(dir), errors.New("evidence manifest write failed")
	}
	_, writeErr := file.Write(encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return filepath.Base(dir), errors.New("evidence manifest write failed")
	}
	return filepath.Base(dir), nil
}

// LoadFrameEvidence reads only the allowlisted PGM format emitted above.
func LoadFrameEvidence(dir string) ([]GrayFrame, []GrayFrame, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	info, err := os.Stat(manifestPath)
	if err != nil || info.Size() > 1<<20 {
		return nil, nil, errors.New("evidence manifest unavailable")
	}
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, nil, errors.New("evidence manifest unavailable")
	}
	var manifest evidenceManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil || manifest.Format != evidenceFormat || len(manifest.Baseline) > maxEvidenceFrames || len(manifest.Quiet) > maxEvidenceFrames {
		return nil, nil, errors.New("evidence manifest invalid")
	}
	readFrames := func(refs []evidenceFrame) ([]GrayFrame, error) {
		frames := make([]GrayFrame, 0, len(refs))
		for _, ref := range refs {
			if ref.Name == "" || filepath.Base(ref.Name) != ref.Name || filepath.Ext(ref.Name) != ".pgm" {
				return nil, errors.New("evidence frame name invalid")
			}
			frame, err := readPGM(filepath.Join(dir, ref.Name), ref.At)
			if err != nil {
				return nil, errors.New("evidence frame invalid")
			}
			frames = append(frames, frame)
		}
		return frames, nil
	}
	baseline, err := readFrames(manifest.Baseline)
	if err != nil {
		return nil, nil, err
	}
	quiet, err := readFrames(manifest.Quiet)
	if err != nil {
		return nil, nil, err
	}
	return baseline, quiet, nil
}

func readPGM(path string, at time.Time) (GrayFrame, error) {
	file, err := os.Open(path)
	if err != nil {
		return GrayFrame{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > maxEvidenceFrameSize+128 {
		return GrayFrame{}, errors.New("evidence frame size invalid")
	}
	reader := bufio.NewReader(io.LimitReader(file, maxEvidenceFrameSize+129))
	magic, err := readPGMToken(reader)
	if err != nil || magic != "P5" {
		return GrayFrame{}, errors.New("evidence format invalid")
	}
	widthText, err := readPGMToken(reader)
	if err != nil {
		return GrayFrame{}, errors.New("evidence width invalid")
	}
	heightText, err := readPGMToken(reader)
	if err != nil {
		return GrayFrame{}, errors.New("evidence height invalid")
	}
	maxText, err := readPGMToken(reader)
	if err != nil || maxText != "255" {
		return GrayFrame{}, errors.New("evidence depth invalid")
	}
	var width, height int
	if _, err := fmt.Sscanf(widthText, "%d", &width); err != nil {
		return GrayFrame{}, errors.New("evidence width invalid")
	}
	if _, err := fmt.Sscanf(heightText, "%d", &height); err != nil {
		return GrayFrame{}, errors.New("evidence height invalid")
	}
	if width < 1 || height < 1 || width > 4096 || height > 4096 || width*height > maxEvidenceFrameSize {
		return GrayFrame{}, errors.New("evidence dimensions invalid")
	}
	pixels := make([]byte, width*height)
	if _, err := io.ReadFull(reader, pixels); err != nil {
		return GrayFrame{}, errors.New("evidence pixels truncated")
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return GrayFrame{}, errors.New("evidence pixels have trailing data")
	}
	frame := GrayFrame{At: at, Width: width, Height: height, Pixels: pixels}
	if !validFrame(frame) {
		return GrayFrame{}, errors.New("evidence frame invalid")
	}
	return frame, nil
}

func readPGMToken(reader *bufio.Reader) (string, error) {
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b > ' ' {
			if err := reader.UnreadByte(); err != nil {
				return "", err
			}
			break
		}
	}
	var token strings.Builder
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b <= ' ' {
			if b == '\r' {
				next, nextErr := reader.ReadByte()
				if nextErr == nil && next != '\n' {
					_ = reader.UnreadByte()
				}
			}
			return token.String(), nil
		}
		if token.Len() >= 16 {
			return "", errors.New("evidence token too long")
		}
		token.WriteByte(b)
	}
}

func cloneFrames(frames []GrayFrame) []GrayFrame {
	clone := make([]GrayFrame, len(frames))
	for i, frame := range frames {
		clone[i] = frame
		clone[i].Pixels = append([]byte(nil), frame.Pixels...)
	}
	return clone
}
