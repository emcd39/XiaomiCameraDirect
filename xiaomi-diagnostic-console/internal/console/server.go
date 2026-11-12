package console

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed static/*
var assets embed.FS

type event struct {
	Time    string `json:"time"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type Server struct {
	cfg         Config
	gateway     *Gateway
	video       *Video
	recording   Recording
	ptz         PTZ
	cameraMu    sync.Mutex
	mu          sync.Mutex
	events      []event
	httpSrv     *http.Server
	listenReady chan struct{}
}

func NewServer(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, gateway: newGateway(cfg), video: newVideo(cfg.Mock), listenReady: make(chan struct{})}, nil
}
func (s *Server) event(kind, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event{Time: time.Now().UTC().Format(time.RFC3339), Kind: kind, Message: message})
	if len(s.events) > 100 {
		s.events = s.events[len(s.events)-100:]
	}
}
func (s *Server) Run() error {
	h, err := s.Handler()
	if err != nil {
		return err
	}
	listener, err := listenConfigured(s.cfg.Listen, s.cfg.ListenFallback)
	if err != nil {
		return err
	}
	s.cfg.Listen = listener.Addr().String()
	s.httpSrv = &http.Server{Addr: s.cfg.Listen, Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	close(s.listenReady)
	log.Printf("Diagnostic Console listening on http://%s (mock=%v; PTZ enabled=%v)", s.cfg.Listen, s.cfg.Mock, s.cfg.PTZ.Enabled)
	err = s.httpSrv.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func listenConfigured(address string, fallback bool) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err == nil || !fallback || !isAddressInUse(err) {
		return listener, err
	}
	host, _, splitErr := net.SplitHostPort(address)
	if splitErr != nil {
		return nil, err
	}
	fallbackAddress := net.JoinHostPort(host, "0")
	listener, fallbackErr := net.Listen("tcp", fallbackAddress)
	if fallbackErr != nil {
		return nil, fmt.Errorf("preferred loopback address is busy and fallback bind failed: %w", fallbackErr)
	}
	return listener, nil
}

func isAddressInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && uint32(errno) == 10048 { // WSAEADDRINUSE
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") || strings.Contains(message, "only one usage of each socket address")
}
func (s *Server) Shutdown(ctx context.Context) error {
	s.video.Stop()
	s.recording.Stop()
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) Handler() (http.Handler, error) {
	static, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/status", s.getStatus)
	mux.HandleFunc("/api/events", s.getEvents)
	mux.HandleFunc("/api/capabilities", s.getCapabilities)
	mux.HandleFunc("/api/inventory/refresh", s.refreshInventory)
	mux.HandleFunc("/api/inventory/select", s.selectCamera)
	mux.HandleFunc("/api/video/start", s.videoStart)
	mux.HandleFunc("/api/video/stop", s.videoStop)
	mux.HandleFunc("/api/video/mjpeg", s.streamMJPEG)
	mux.HandleFunc("/api/record/start", s.recordStart)
	mux.HandleFunc("/api/record/stop", s.recordStop)
	mux.HandleFunc("/api/record/files", s.recordList)
	mux.HandleFunc("/api/record/download/", s.recordDownload)
	mux.HandleFunc("/api/ptz/left", s.ptzLeft)
	mux.HandleFunc("/api/ptz/step", s.ptzStep)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		// Prevent DNS-rebinding against the localhost-only service.
		if !strings.EqualFold(r.Host, s.cfg.Listen) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "http://"+s.cfg.Listen {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
			if r.Header.Get("X-XLG-Action") != "1" {
				http.Error(w, "missing action header", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	}), nil
}
func jsonOut(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
func fail(w http.ResponseWriter, code int, what string) {
	jsonOut(w, code, map[string]any{"error": what})
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		fail(w, 405, "method not allowed")
		return false
	}
	return true
}
func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	g := GatewayStatus{}
	if s.cfg.Mock {
		s.gateway.mu.RLock()
		inventory := s.gateway.inventorySummary
		s.gateway.mu.RUnlock()
		g = GatewayStatus{Mode: "mock", Reachable: true, HTTPStatus: 200, InfoStatus: 200, Inventory: inventory, Note: "SIMULATION: no camera, network call or CS2 session"}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
		defer cancel()
		g = s.gateway.Status(ctx)
	}
	jsonOut(w, 200, map[string]any{"mock": s.cfg.Mock, "gateway": g, "video": s.video.Status(), "recording": s.recording.Status(s.cfg), "ptz": map[string]any{"enabled": s.cfg.Mock || s.cfg.PTZ.Enabled, "busy": s.ptz.Busy(), "last": s.ptz.Last()}, "camera_address_verified": false})
}
func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	s.mu.Lock()
	list := append([]event(nil), s.events...)
	s.mu.Unlock()
	if list == nil {
		list = []event{}
	}
	jsonOut(w, 200, list)
}
func (s *Server) getCapabilities(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	jsonOut(w, 200, []map[string]string{
		{"name": "RTSP video (H.265)", "state": "historically_verified", "description": "Previously decoded 848x480 from C201; not verified against current IP/runtime."},
		{"name": "PTZ single steps", "state": "live_verified", "description": "Left, right, up and down were each sent as one live step; physical movement was observed on the C201."},
		{"name": "Local NAS recording", "state": "available_when_configured", "description": "Explicit FFmpeg recording of gateway RTSP into 5-minute Matroska segments. Does not read camera SD."},
		{"name": "Camera SD card", "state": "unknown", "description": "SD presence/capacity/list/recordings must be researched and verified. No SD read/write endpoint wired."},
		{"name": "Motion events", "state": "unknown", "description": "Not yet connected to a local MIoT/CS2 event source."},
		{"name": "Night IR / image settings", "state": "unknown", "description": "Read-only discovery planned; no unknown property writes."},
		{"name": "Audio / talkback", "state": "unknown", "description": "No verified audio stream or motor-independent talkback control."},
		{"name": "Cloud-free session negotiation", "state": "not_verified", "description": "Current CS2 setup may require Xiaomi Cloud to negotiate a session."},
	})
}
func (s *Server) refreshInventory(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	s.cameraMu.Lock()
	defer s.cameraMu.Unlock()
	if !s.video.Stop() {
		fail(w, http.StatusServiceUnavailable, "preview is still stopping; camera inventory was not changed")
		return
	}
	if s.cfg.Mock {
		option := CameraOption{ID: "xiaomi_0000000000000001", Name: "Mock C201"}
		inv := InventorySummary{Loaded: true, CameraCount: 1, C201Count: 1, C201Cameras: []CameraOption{option}, SelectedCameraID: option.ID, SessionReady: true, SessionState: "streaming", FetchedAt: time.Now().UTC().Format(time.RFC3339)}
		s.gateway.mu.Lock()
		s.gateway.inventorySummary = inv
		s.gateway.cameras = map[string]gatewayCamera{option.ID: {option: option, model: "xiaomi.camera.c201", videoState: "streaming", rtspURL: "rtsp://127.0.0.1/mock"}}
		s.gateway.selectedCameraID = option.ID
		s.gateway.mu.Unlock()
		s.event("inventory", "mock inventory refreshed (no camera request)")
		jsonOut(w, 200, inv)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	inv, err := s.gateway.RefreshInventory(ctx)
	if err != nil {
		s.event("inventory", "explicit inventory refresh failed")
		fail(w, 502, "gateway inventory refresh failed; see gateway log")
		return
	}
	if inv.C201Count == 1 {
		if err := s.gateway.PrepareSelected(ctx); err != nil {
			inv = s.gateway.inventorySnapshot()
			s.event("inventory", "C201 inventory loaded; camera session is not ready")
			jsonOut(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "inventory": inv})
			return
		}
		inv = s.gateway.inventorySnapshot()
	}
	s.event("inventory", "C201 inventory loaded; selected session prepared when unique")
	jsonOut(w, 200, inv)
}

func (s *Server) selectCamera(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var body struct {
		CameraID string `json:"camera_id"`
	}
	if !decodeActionJSON(w, r, &body) || body.CameraID == "" {
		return
	}
	s.cameraMu.Lock()
	defer s.cameraMu.Unlock()
	if !s.video.Stop() {
		fail(w, http.StatusServiceUnavailable, "preview is still stopping; camera selection was not changed")
		return
	}
	if s.cfg.Mock {
		if err := s.gateway.SelectCamera(body.CameraID); err != nil {
			fail(w, http.StatusNotFound, "camera_not_found")
			return
		}
		jsonOut(w, http.StatusOK, s.gateway.inventorySnapshot())
		return
	}
	if err := s.gateway.SelectCamera(body.CameraID); err != nil {
		fail(w, http.StatusNotFound, "camera_not_found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	if err := s.gateway.PrepareSelected(ctx); err != nil {
		inv := s.gateway.inventorySnapshot()
		jsonOut(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "inventory": inv})
		return
	}
	s.event("inventory", "selected C201 camera session prepared")
	jsonOut(w, http.StatusOK, s.gateway.inventorySnapshot())
}
func (s *Server) videoStart(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	s.cameraMu.Lock()
	defer s.cameraMu.Unlock()
	if err := s.video.Start(s.cfg, s.gateway); err != nil {
		fail(w, 409, err.Error())
		return
	}
	s.event("video", "preview manually started")
	jsonOut(w, 200, map[string]string{"result": "started"})
}
func (s *Server) videoStop(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	s.cameraMu.Lock()
	defer s.cameraMu.Unlock()
	if !s.video.Stop() {
		fail(w, http.StatusServiceUnavailable, "preview worker did not stop in time")
		return
	}
	s.event("video", "preview stopped")
	jsonOut(w, 200, map[string]string{"result": "stopped"})
}
func (s *Server) streamMJPEG(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store")
	f, ok := w.(http.Flusher)
	if !ok {
		return
	}
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	var last uint64
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if !s.video.Status().Running {
				return
			}
			frame, seq := s.video.Latest()
			if seq == last || len(frame) == 0 {
				continue
			}
			last = seq
			if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame)); err != nil {
				return
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			if _, err := w.Write([]byte("\r\n")); err != nil {
				return
			}
			f.Flush()
		}
	}
}
func (s *Server) recordStart(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	if err := s.recording.Start(s.cfg, s.gateway); err != nil {
		fail(w, 409, err.Error())
		return
	}
	s.event("record", "local MKV recording started explicitly")
	jsonOut(w, 200, map[string]string{"result": "started"})
}
func (s *Server) recordStop(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	s.recording.Stop()
	s.event("record", "local recording stopped")
	jsonOut(w, 200, map[string]string{"result": "stopped"})
}
func (s *Server) recordList(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	files, err := listRecordings(s.cfg.RecordDir)
	if err != nil {
		fail(w, 500, "cannot list recordings")
		return
	}
	jsonOut(w, 200, files)
}
func (s *Server) recordDownload(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/record/download/")
	if !validRecordingFilename(name) || filepath.Base(name) != name {
		fail(w, 400, "invalid filename")
		return
	}
	path := filepath.Join(s.cfg.RecordDir, name)
	// Deny links so an untrusted recording entry cannot point at a token file.
	linkInfo, linkErr := os.Lstat(path)
	if linkErr != nil || !linkInfo.Mode().IsRegular() {
		fail(w, 404, "recording unavailable")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		fail(w, 404, "recording not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		fail(w, 404, "recording unavailable")
		return
	}
	w.Header().Set("Content-Type", "video/x-matroska")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	http.ServeContent(w, r, name, st.ModTime(), f)
}
func (s *Server) ptzLeft(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	if !decodeOptionalEmptyJSON(w, r) {
		return
	}
	cameraID := ""
	if s.gateway != nil {
		cameraID = s.gateway.SelectedCameraID()
	}
	s.runPTZStep(w, cameraID, "left")
}

func (s *Server) ptzStep(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var body struct {
		CameraID  string `json:"camera_id"`
		Direction string `json:"direction"`
	}
	if !decodeActionJSON(w, r, &body) {
		return
	}
	if body.Direction != "left" && body.Direction != "right" && body.Direction != "up" && body.Direction != "down" {
		fail(w, http.StatusBadRequest, "invalid_direction")
		return
	}
	if body.CameraID == "" {
		fail(w, http.StatusBadRequest, "camera_id is required")
		return
	}
	s.runPTZStep(w, body.CameraID, body.Direction)
}

func (s *Server) runPTZStep(w http.ResponseWriter, cameraID, direction string) {
	if s.cfg.PTZ.Enabled == false && !s.cfg.Mock {
		fail(w, http.StatusForbidden, "live PTZ disabled in configuration")
		return
	}
	result, err := s.ptz.RunOnce(s.cfg, s.gateway, cameraID, direction)
	if err != nil {
		s.event("ptz", direction+" step failed; no automatic retry")
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "camera selection changed") {
			status = http.StatusConflict
		}
		if strings.Contains(err.Error(), "session") || strings.Contains(err.Error(), "select a C201") {
			status = http.StatusServiceUnavailable
		}
		jsonOut(w, status, map[string]any{"error": err.Error(), "result": result})
		return
	}
	if s.cfg.Mock {
		s.event("ptz", "mock "+direction+" step simulated; no actual motor command")
	} else {
		s.event("ptz", "one "+direction+" step accepted by managed gateway; physical motion not confirmed by HTTP")
	}
	jsonOut(w, 200, result)
}

func decodeOptionalEmptyJSON(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return true
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&struct{}{}); err != nil || d.Decode(&struct{}{}) != io.EOF {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func decodeActionJSON(w http.ResponseWriter, r *http.Request, body any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(body); err != nil || d.Decode(&struct{}{}) != io.EOF {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}
