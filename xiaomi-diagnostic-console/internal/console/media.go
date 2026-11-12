package console

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	Error               string `json:"error,omitempty"`
	DirectoryConfigured bool   `json:"directory_configured"`
}
type Recording struct {
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	errText string
}

func (r *Recording) Status(c Config) RecordStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RecordStatus{Running: r.running, Error: r.errText, DirectoryConfigured: c.RecordDir != ""}
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
		return err
	}
	filename := filepath.Join(c.RecordDir, "c201_%Y%m%d_%H%M%S.mkv")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, c.FFmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-rtsp_transport", "tcp", "-i", raw, "-map", "0:v:0", "-an", "-c:v", "copy", "-f", "segment", "-segment_time", "300", "-segment_format", "matroska", "-strftime", "1", "-reset_timestamps", "1", filename)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	r.running = true
	r.errText = ""
	r.cancel = cancel
	go func() {
		err := cmd.Wait()
		r.mu.Lock()
		r.running = false
		r.cancel = nil
		if err != nil && ctx.Err() == nil {
			r.errText = "recording stopped unexpectedly; inspect FFmpeg configuration"
		}
		r.mu.Unlock()
	}()
	return nil
}
func (r *Recording) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// The wait goroutine owns the transition to stopped. A new Start cannot
	// race with the previous FFmpeg process while its files are still open.
}

type RecordingFile struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

func listRecordings(dir string) ([]RecordingFile, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []RecordingFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var files []RecordingFile
	for _, e := range entries {
		if e.IsDir() || !validRecordingFilename(e.Name()) {
			continue
		}
		inf, err := e.Info()
		if err != nil || !inf.Mode().IsRegular() {
			continue
		}
		files = append(files, RecordingFile{Name: e.Name(), Size: inf.Size(), ModifiedAt: inf.ModTime().UTC().Format(time.RFC3339)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModifiedAt > files[j].ModifiedAt })
	if len(files) > 200 {
		files = files[:200]
	}
	return files, nil
}
func validRecordingFilename(name string) bool {
	if strings.ContainsAny(name, "/\\") || !strings.HasPrefix(name, "c201_") || !strings.HasSuffix(name, ".mkv") {
		return false
	}
	for _, r := range strings.TrimSuffix(strings.TrimPrefix(name, "c201_"), ".mkv") {
		if r != '_' && (r < '0' || r > '9') {
			return false
		}
	}
	return len(name) >= len("c201_20060102_150405.mkv")
}
