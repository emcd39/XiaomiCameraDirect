package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxFrameBytes = 4 << 20

type VideoStatus struct {
	Running     bool    `json:"running"`
	Mock        bool    `json:"mock"`
	Frames      uint64  `json:"frames"`
	FPS         float64 `json:"fps"`
	LastFrameAt string  `json:"last_frame_at,omitempty"`
	Error       string  `json:"error,omitempty"`
	Resolution  string  `json:"resolution"`
}

type Video struct {
	mu         sync.RWMutex
	running    bool
	stopping   bool
	done       chan struct{}
	latest     []byte
	frames     uint64
	lastFrame  time.Time
	fps        float64
	errText    string
	cancel     context.CancelFunc
	generation uint64
	mock       bool
}

func newVideo(mock bool) *Video { return &Video{mock: mock} }
func (v *Video) Status() VideoStatus {
	v.mu.RLock()
	defer v.mu.RUnlock()
	s := VideoStatus{Running: v.running, Mock: v.mock, Frames: v.frames, FPS: v.fps, Error: v.errText, Resolution: "848x480 (preview may be scaled)"}
	if !v.lastFrame.IsZero() {
		s.LastFrameAt = v.lastFrame.UTC().Format(time.RFC3339Nano)
	}
	return s
}
func (v *Video) Latest() ([]byte, uint64) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.latest, v.frames
}
func (v *Video) acceptFrame(gen uint64, b []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.running || v.generation != gen {
		return
	}
	now := time.Now()
	if !v.lastFrame.IsZero() {
		interval := now.Sub(v.lastFrame).Seconds()
		if interval > 0 {
			instant := 1 / interval
			if v.fps == 0 {
				v.fps = instant
			} else {
				v.fps = 0.7*v.fps + 0.3*instant
			}
		}
	}
	v.latest = append([]byte(nil), b...)
	v.lastFrame = now
	v.frames++
}
func (v *Video) Start(c Config, g *Gateway) error {
	v.mu.Lock()
	if v.running || v.stopping {
		v.mu.Unlock()
		return errors.New("preview worker already running or stopping")
	}
	var rtsp string
	var err error
	if !c.Mock {
		rtsp, err = g.RTSPURL()
		if err != nil {
			v.mu.Unlock()
			return err
		}
		if _, err = exec.LookPath(c.FFmpegPath); err != nil {
			v.mu.Unlock()
			return errors.New("ffmpeg executable not found")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.done = make(chan struct{})
	v.running = true
	v.frames = 0
	v.fps = 0
	v.latest = nil
	v.lastFrame = time.Time{}
	v.errText = ""
	v.generation++
	gen := v.generation
	v.mu.Unlock()
	go func() {
		var e error
		if c.Mock {
			e = v.runMock(ctx, gen)
		} else {
			e = v.runFFmpeg(ctx, gen, c.FFmpegPath, rtsp)
		}
		v.mu.Lock()
		if v.generation == gen {
			v.running = false
			v.stopping = false
			v.cancel = nil
			v.latest = nil
			v.lastFrame = time.Time{}
			if e != nil && !errors.Is(e, context.Canceled) {
				v.errText = "preview stopped; inspect local FFmpeg stderr outside this UI"
			}
		}
		close(v.done)
		v.mu.Unlock()
	}()
	return nil
}
func (v *Video) Stop() bool {
	v.mu.Lock()
	cancel := v.cancel
	done := v.done
	stopped := !v.running && !v.stopping
	if cancel != nil {
		v.stopping = true
	} else {
		v.latest = nil
		v.lastFrame = time.Time{}
		stopped = !v.running && !v.stopping
	}
	v.mu.Unlock()
	if cancel == nil {
		return stopped
	}
	cancel()
	// Never allow two overlapping preview workers. The worker clears stopping
	// only after FFmpeg exits (or mock generation ends).
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return false
	}
	v.mu.RLock()
	stopped = !v.running && !v.stopping
	v.mu.RUnlock()
	return stopped
}

func (v *Video) runFFmpeg(ctx context.Context, gen uint64, binary, rawRTSP string) error {
	cmd := exec.CommandContext(ctx, binary, "-hide_banner", "-loglevel", "error", "-nostdin", "-rtsp_transport", "tcp", "-i", rawRTSP, "-an", "-vf", "fps=3,scale=848:480", "-q:v", "6", "-f", "mjpeg", "pipe:1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard // never expose capability URL from ffmpeg errors
	if err := cmd.Start(); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(stdout, 128<<10)
	for {
		frame, e := readJPEGFrame(reader, maxFrameBytes)
		if e != nil {
			break
		}
		v.acceptFrame(gen, frame)
		select {
		case <-ctx.Done():
			_ = cmd.Wait()
			return ctx.Err()
		default:
		}
	}
	e := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil {
		return e
	}
	return fmt.Errorf("decoder output ended")
}

// readJPEGFrame consumes a concatenated JPEG pipe; it never trusts a frame length.
func readJPEGFrame(r *bufio.Reader, maxSize int) ([]byte, error) {
	prev := byte(0)
	for {
		b, e := r.ReadByte()
		if e != nil {
			return nil, e
		}
		if prev == 0xff && b == 0xd8 {
			break
		}
		prev = b
	}
	frame := []byte{0xff, 0xd8}
	prev = 0xd8
	for len(frame) < maxSize {
		b, e := r.ReadByte()
		if e != nil {
			return nil, e
		}
		frame = append(frame, b)
		if prev == 0xff && b == 0xd9 {
			return frame, nil
		}
		prev = b
	}
	return nil, errors.New("JPEG frame larger than limit")
}

func (v *Video) runMock(ctx context.Context, gen uint64) error {
	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			n++
			img := image.NewRGBA(image.Rect(0, 0, 848, 480))
			draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 22, G: 32, B: 46, A: 255}}, image.Point{}, draw.Src)
			for x := 0; x < 848; x += 53 {
				for y := 0; y < 480; y++ {
					img.Set(x, y, color.RGBA{R: 48, G: 62, B: 75, A: 255})
				}
			}
			for y := 0; y < 480; y += 40 {
				for x := 0; x < 848; x++ {
					img.Set(x, y, color.RGBA{R: 48, G: 62, B: 75, A: 255})
				}
			}
			cx := 170 + (n*6)%530
			cy := 240
			for y := -26; y <= 26; y++ {
				for x := -26; x <= 26; x++ {
					if x*x+y*y <= 26*26 {
						img.Set(cx+x, cy+y, color.RGBA{R: 75, G: 190, B: 176, A: 255})
					}
				}
			}
			var b bytes.Buffer
			if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 72}); err != nil {
				return err
			}
			v.acceptFrame(gen, b.Bytes())
		}
	}
}

type RecordStatus struct {
	Running             bool   `json:"running"`
	Reconnecting        bool   `json:"reconnecting,omitempty"`
	ReconnectAttempt    int    `json:"reconnect_attempt,omitempty"`
	Error               string `json:"error,omitempty"`
	DirectoryConfigured bool   `json:"directory_configured"`
}
type recordingRun struct {
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stderr        *recordingDiagnosticBuffer
	done          chan struct{}
	stopSignal    chan struct{}
	stopRequested bool
	forcedStop    bool
	result        error
}

// recordingDiagnosticBuffer keeps a bounded prefix of FFmpeg stderr. Raw
// diagnostics are only classified locally because they may contain the
// managed RTSP capability URL.
type recordingDiagnosticBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *recordingDiagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		b.limit = 16 << 10
	}
	n := len(p)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		b.data = append(b.data, p[:remaining]...)
	}
	return n, nil
}

func (b *recordingDiagnosticBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func classifyRecordingFailure(processErr error, diagnostic string) string {
	text := strings.ToLower(diagnostic + " " + errorString(processErr))
	switch {
	case strings.Contains(text, "no space left on device"), strings.Contains(text, "disk full"), strings.Contains(text, "not enough space"), strings.Contains(text, "error_disk_full"):
		return "recording storage is full; incomplete segment is hidden; reconnect will be retried"
	case strings.Contains(text, "read-only file system"), strings.Contains(text, "network path was not found"), strings.Contains(text, "network path not found"), strings.Contains(text, "network name cannot be found"), strings.Contains(text, "network name is no longer available"), strings.Contains(text, "network access is denied"):
		return "recording storage is unavailable or not writable; incomplete segment is hidden; reconnect will be retried"
	default:
		return "RTSP recording stopped; reconnecting with a new segment"
	}
}

func classifyRecordingStartFailure(err error, recordDir string) string {
	return classifyRecordingStartFailureWithStat(err, recordDir, os.Stat)
}

func classifyRecordingStartFailureWithStat(err error, recordDir string, stat func(string) (os.FileInfo, error)) string {
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return "RTSP reconnect failed; retrying"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Op == "chdir" {
		return "recording storage is unavailable or not writable; incomplete segment is hidden; reconnect will be retried"
	}
	// Some platforms do not preserve the failing operation in the returned
	// error. In that ambiguous case, classify the directory state itself. The
	// caller must do this filesystem access without holding Recording.mu.
	info, statErr := stat(recordDir)
	if statErr != nil || !info.IsDir() {
		return "recording storage is unavailable or not writable; incomplete segment is hidden; reconnect will be retried"
	}
	return "RTSP reconnect failed; retrying"
}

func storageStartError(err error) string {
	if err == nil {
		return ""
	}
	classified := classifyRecordingFailure(err, "")
	if classified != "RTSP recording stopped; reconnecting with a new segment" {
		return strings.TrimSuffix(classified, "; reconnect will be retried")
	}
	return "recording could not start because its storage directory is unavailable or not writable"
}

type Recording struct {
	mu                   sync.Mutex
	running              bool
	reconnecting         bool
	reconnectAttempt     int
	run                  *recordingRun
	errText              string
	commandFactory       func(string, ...string) *exec.Cmd
	retryBaseDelay       time.Duration
	stopTimeout          time.Duration
	beforeStopTimeout    func()
	beforeReconnectStart func()
	afterReconnectStart  func()
	onReconnectStartFail func(error)
	statRecordDir        func(string) (os.FileInfo, error)
}

func (r *Recording) Status(c Config) RecordStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RecordStatus{Running: r.running, Reconnecting: r.reconnecting, ReconnectAttempt: r.reconnectAttempt, Error: r.errText, DirectoryConfigured: c.RecordDir != ""}
}
func (r *Recording) Start(c Config, g *Gateway) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return errors.New("recording already running")
	}
	if c.Mock {
		return errors.New("mock mode does not create fake video files")
	}
	raw, err := g.RTSPURL()
	if err != nil {
		return err
	}
	if _, err = exec.LookPath(c.FFmpegPath); err != nil {
		return errors.New("ffmpeg not found")
	}
	if err := os.MkdirAll(c.RecordDir, 0700); err != nil {
		r.errText = storageStartError(err)
		return err
	}
	runID := fmt.Sprintf("c201_%s_%d", time.Now().UTC().Format("20060102_150405"), time.Now().UTC().UnixNano())
	cmd, stdin, stderr, err := r.newFFmpegCommand(c, raw, runID)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	run := &recordingRun{cmd: cmd, stdin: stdin, stderr: stderr, done: make(chan struct{}), stopSignal: make(chan struct{})}
	r.running = true
	r.reconnecting = false
	r.reconnectAttempt = 0
	r.errText = ""
	r.run = run
	go r.supervise(c, raw, runID, run)
	return nil
}

func (r *Recording) newFFmpegCommand(c Config, raw, runID string) (*exec.Cmd, io.WriteCloser, *recordingDiagnosticBuffer, error) {
	filename := runID + "_%03d.mkv"
	manifest := runID + ".csv"
	args := []string{"-hide_banner", "-loglevel", "error", "-stdin", "-rtsp_transport", "tcp", "-i", raw, "-map", "0:v:0", "-an", "-c:v", "copy", "-f", "segment", "-segment_time", "300", "-segment_format", "matroska", "-segment_list", manifest, "-segment_list_type", "csv", "-reset_timestamps", "1", filename}
	cmd := exec.Command(c.FFmpegPath, args...)
	diagnostics := &recordingDiagnosticBuffer{limit: 16 << 10}
	if r.commandFactory != nil {
		cmd = r.commandFactory(c.FFmpegPath, args...)
		if cmd.Stdout == nil {
			cmd.Stdout = io.Discard
		}
	} else {
		cmd.Stdout = io.Discard
	}
	if cmd.Stderr != nil {
		cmd.Stderr = io.MultiWriter(diagnostics, cmd.Stderr)
	} else {
		cmd.Stderr = diagnostics
	}
	cmd.Dir = c.RecordDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, errors.New("cannot open FFmpeg control input")
	}
	return cmd, stdin, diagnostics, nil
}

func (r *Recording) supervise(c Config, raw, baseRunID string, run *recordingRun) {
	attempt := 0
	for {
		processErr := run.cmd.Wait()
		r.mu.Lock()
		run.cmd = nil
		run.stdin = nil
		if run.stopRequested || r.run != run {
			r.finishLocked(run, processErr)
			r.mu.Unlock()
			return
		}
		attempt++
		r.reconnecting = true
		r.reconnectAttempt = attempt
		r.errText = classifyRecordingFailure(processErr, run.stderr.String())
		if processErr == nil {
			r.errText = "RTSP recording ended; reconnecting with a new segment"
		}
		r.mu.Unlock()

		for {
			timer := time.NewTimer(r.retryDelay(attempt))
			select {
			case <-run.stopSignal:
				timer.Stop()
				r.mu.Lock()
				r.finishLocked(run, nil)
				r.mu.Unlock()
				return
			case <-timer.C:
			}
			select {
			case <-run.stopSignal:
				r.mu.Lock()
				r.finishLocked(run, nil)
				r.mu.Unlock()
				return
			default:
			}

			runID := fmt.Sprintf("%s_reconnect%02d", baseRunID, attempt)
			nextCmd, nextStdin, nextStderr, startErr := r.newFFmpegCommand(c, raw, runID)
			if startErr == nil {
				if r.beforeReconnectStart != nil {
					r.beforeReconnectStart()
				}
				startErr = nextCmd.Start()
				if startErr == nil && r.afterReconnectStart != nil {
					r.afterReconnectStart()
				}
			}
			if startErr == nil {
				r.mu.Lock()
				run.cmd = nextCmd
				run.stdin = nextStdin
				run.stderr = nextStderr
				stopping := run.stopRequested || r.run != run
				forceStop := run.forcedStop
				if r.run == run && !stopping {
					r.reconnecting = false
					r.errText = ""
				}
				r.mu.Unlock()
				if stopping {
					_, _ = io.WriteString(nextStdin, "q\n")
					_ = nextStdin.Close()
				}
				if forceStop {
					_ = nextCmd.Process.Kill()
				}
				break
			}
			if r.onReconnectStartFail != nil {
				r.onReconnectStartFail(startErr)
			}

			if nextStdin != nil {
				_ = nextStdin.Close()
			}
			if r.handleReconnectStartFailure(c, run, startErr) {
				return
			}
			attempt++
			r.mu.Lock()
			if r.run == run {
				r.reconnectAttempt = attempt
			}
			r.mu.Unlock()
		}
	}
}

// handleReconnectStartFailure keeps the potentially slow filesystem check out
// of Recording.mu, then revalidates that this is still the active run and has
// not been stopped before publishing the classification.
func (r *Recording) handleReconnectStartFailure(c Config, run *recordingRun, startErr error) bool {
	r.mu.Lock()
	stopping := run.stopRequested || r.run != run
	r.mu.Unlock()

	var classified string
	if !stopping {
		stat := r.statRecordDir
		if stat == nil {
			stat = os.Stat
		}
		classified = classifyRecordingStartFailureWithStat(startErr, c.RecordDir, stat)
	}

	r.mu.Lock()
	stopping = run.stopRequested || r.run != run
	if r.run == run && !stopping {
		r.errText = classified
	}
	if stopping && r.run == run {
		r.finishLocked(run, nil)
	}
	r.mu.Unlock()
	return stopping
}

func (r *Recording) retryDelay(attempt int) time.Duration {
	delay := r.retryBaseDelay
	if delay <= 0 {
		delay = time.Second
	}
	for i := 1; i < attempt && delay < 30*time.Second; i++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

func (r *Recording) finishLocked(run *recordingRun, processErr error) {
	run.result = nil
	if run.forcedStop {
		run.result = errors.New("recording did not finalize cleanly; incomplete segment is hidden")
	} else if processErr != nil {
		run.result = errors.New("recording stopped unexpectedly; incomplete files are hidden")
	}
	if r.run == run {
		r.running = false
		r.reconnecting = false
		r.reconnectAttempt = 0
		r.run = nil
		if run.forcedStop {
			r.errText = "recording stopped before the active segment was finalized; incomplete files are hidden"
		} else if processErr != nil {
			r.errText = "recording stopped unexpectedly; incomplete files are hidden"
		} else {
			r.errText = ""
		}
	}
	close(run.done)
}

func (r *Recording) Stop() error {
	r.mu.Lock()
	run := r.run
	if run == nil || !r.running {
		r.mu.Unlock()
		return nil
	}
	shouldSignal := !run.stopRequested
	run.stopRequested = true
	stdin := run.stdin
	if shouldSignal {
		close(run.stopSignal)
	}
	r.mu.Unlock()
	if shouldSignal && stdin != nil {
		_, _ = io.WriteString(stdin, "q\n")
		_ = stdin.Close()
	}
	timeout := r.stopTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-run.done:
		return run.result
	case <-timer.C:
	}
	// This fast-path is deliberately followed by a locked recheck below: the
	// run can finish and a new Start can publish another run in between them.
	select {
	case <-run.done:
		return run.result
	default:
	}
	if r.beforeStopTimeout != nil {
		r.beforeStopTimeout()
	}
	r.mu.Lock()
	select {
	case <-run.done:
		r.mu.Unlock()
		return run.result
	default:
	}
	var killErr error
	if run.cmd != nil && run.cmd.Process != nil {
		run.forcedStop = true
		killErr = run.cmd.Process.Kill()
	} else {
		// The reconnect may be between command construction and publication.
		// Carry the force-stop request into supervise() so that it kills that
		// process as soon as Start returns and the command becomes available.
		run.forcedStop = true
	}
	r.mu.Unlock()
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return errors.New("recording did not finalize cleanly; process could not be terminated")
	}
	<-run.done
	return run.result
}

type RecordingFile struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

func listRecordings(dir string) ([]RecordingFile, error) {
	return listRecordingsWithLegacyVerifier(dir, nil)
}

func listRecordingsWithLegacyVerifier(dir string, verifyLegacy func(string) bool) ([]RecordingFile, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []RecordingFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	completed, err := completedRecordingNames(dir, entries)
	if err != nil {
		return nil, err
	}
	var files []RecordingFile
	for _, e := range entries {
		if e.IsDir() || !validRecordingFilename(e.Name()) {
			continue
		}
		inf, err := e.Info()
		if err != nil || !inf.Mode().IsRegular() || inf.Size() <= 0 {
			continue
		}
		if _, ok := completed[e.Name()]; !ok {
			if !isLegacyRecordingFilename(e.Name()) || verifyLegacy == nil || !verifyLegacy(filepath.Join(dir, e.Name())) {
				continue
			}
		}
		files = append(files, RecordingFile{Name: e.Name(), Size: inf.Size(), ModifiedAt: inf.ModTime().UTC().Format(time.RFC3339)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModifiedAt > files[j].ModifiedAt })
	if len(files) > 200 {
		files = files[:200]
	}
	if files == nil {
		files = []RecordingFile{}
	}
	return files, nil
}

func isLegacyRecordingFilename(name string) bool {
	if !validRecordingFilename(name) {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "c201_"), ".mkv"), "_")
	return len(parts) == 2
}

type ffprobeSummary struct {
	Streams []struct {
		CodecType     string          `json:"codec_type"`
		NBReadPackets json.RawMessage `json:"nb_read_packets"`
	} `json:"streams"`
	Format struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

func parseFFprobeLegacySummary(data []byte) bool {
	var summary ffprobeSummary
	if json.Unmarshal(data, &summary) != nil {
		return false
	}
	duration, err := strconv.ParseFloat(summary.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return false
	}
	if !strings.Contains(summary.Format.FormatName, "matroska") {
		return false
	}
	for _, stream := range summary.Streams {
		if stream.CodecType != "video" || len(stream.NBReadPackets) == 0 {
			continue
		}
		var text string
		if json.Unmarshal(stream.NBReadPackets, &text) != nil {
			text = string(stream.NBReadPackets)
		}
		packets, err := strconv.ParseInt(strings.Trim(text, `"`), 10, 64)
		if err == nil && packets > 0 {
			return true
		}
	}
	return false
}

func parseFFprobeLegacyOutput(stdout, stderr []byte) bool {
	if strings.TrimSpace(string(stderr)) != "" {
		return false
	}
	return parseFFprobeLegacySummary(stdout)
}

func newLegacyRecordingVerifier(ffmpegPath string) func(string) bool {
	return newLegacyRecordingVerifierWithCommand(ffmpegPath, exec.CommandContext)
}

type legacyProbeCommandFactory func(context.Context, string, ...string) *exec.Cmd

func newLegacyRecordingVerifierWithCommand(ffmpegPath string, commandFactory legacyProbeCommandFactory) func(string) bool {
	probePath := ""
	if resolved, err := exec.LookPath(ffmpegPath); err == nil {
		sibling := filepath.Join(filepath.Dir(resolved), "ffprobe"+filepath.Ext(resolved))
		if _, statErr := os.Stat(sibling); statErr == nil {
			probePath = sibling
		}
	}
	if probePath == "" {
		candidate := "ffprobe"
		if filepath.Ext(ffmpegPath) != "" {
			candidate += filepath.Ext(ffmpegPath)
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			probePath = resolved
		}
	}
	if probePath == "" {
		return nil
	}
	if commandFactory == nil {
		commandFactory = exec.CommandContext
	}
	return func(path string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := commandFactory(ctx, probePath, "-v", "error", "-count_packets", "-show_entries", "stream=codec_type,nb_read_packets:format=duration,format_name", "-of", "json", path)
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		output, err := cmd.Output()
		return err == nil && ctx.Err() == nil && parseFFprobeLegacyOutput(output, diagnostics.Bytes())
	}
}

func completedRecordingNames(dir string, entries []os.DirEntry) (map[string]struct{}, error) {
	completed := make(map[string]struct{})
	for _, entry := range entries {
		manifestName := entry.Name()
		if !validRecordingManifestFilename(manifestName) {
			continue
		}
		info, err := os.Lstat(filepath.Join(dir, manifestName))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(filepath.Join(dir, manifestName))
		if err != nil {
			return nil, errors.New("cannot read recording completion manifest")
		}
		reader := csv.NewReader(file)
		reader.FieldsPerRecord = -1
		runID := strings.TrimSuffix(manifestName, ".csv")
		for {
			row, readErr := reader.Read()
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				// FFmpeg may have been interrupted while appending the final row.
				break
			}
			if len(row) != 3 || !validSegmentEntry(runID, row[0], row[1], row[2]) {
				continue
			}
			completed[row[0]] = struct{}{}
		}
		_ = file.Close()
	}
	return completed, nil
}

func validSegmentEntry(runID, name, startText, endText string) bool {
	if filepath.Base(name) != name || !strings.HasPrefix(name, runID+"_") || !validRecordingFilename(name) {
		return false
	}
	start, err := strconv.ParseFloat(startText, 64)
	if err != nil || math.IsNaN(start) || math.IsInf(start, 0) || start < 0 {
		return false
	}
	end, err := strconv.ParseFloat(endText, 64)
	return err == nil && !math.IsNaN(end) && !math.IsInf(end, 0) && end > start
}

func validRecordingManifestFilename(name string) bool {
	if filepath.Base(name) != name || !strings.HasPrefix(name, "c201_") || !strings.HasSuffix(name, ".csv") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "c201_"), ".csv"), "_")
	if len(parts) == 3 {
		return digitsOfLength(parts[0], 8) && len(parts[0]) == 8 && digitsOfLength(parts[1], 6) && len(parts[1]) == 6 && digitsOfLength(parts[2], 1)
	}
	return len(parts) == 4 && len(parts[0]) == 8 && digitsOfLength(parts[0], 8) && len(parts[1]) == 6 && digitsOfLength(parts[1], 6) && digitsOfLength(parts[2], 1) && validReconnectPart(parts[3])
}

func validReconnectPart(value string) bool {
	return strings.HasPrefix(value, "reconnect") && digitsOfLength(strings.TrimPrefix(value, "reconnect"), 2)
}

func digitsOfLength(value string, minLength int) bool {
	if len(value) < minLength {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func validRecordingFilename(name string) bool {
	if strings.ContainsAny(name, "/\\") || !strings.HasPrefix(name, "c201_") || !strings.HasSuffix(name, ".mkv") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "c201_"), ".mkv"), "_")
	if len(parts) == 2 {
		return digitsOfLength(parts[0], 8) && len(parts[0]) == 8 && digitsOfLength(parts[1], 6) && len(parts[1]) == 6
	}
	if len(parts) == 4 {
		return len(parts[0]) == 8 && digitsOfLength(parts[0], 8) && len(parts[1]) == 6 && digitsOfLength(parts[1], 6) && digitsOfLength(parts[2], 1) && digitsOfLength(parts[3], 3)
	}
	return len(parts) == 5 && len(parts[0]) == 8 && digitsOfLength(parts[0], 8) && len(parts[1]) == 6 && digitsOfLength(parts[1], 6) && digitsOfLength(parts[2], 1) && validReconnectPart(parts[3]) && digitsOfLength(parts[4], 3)
}
