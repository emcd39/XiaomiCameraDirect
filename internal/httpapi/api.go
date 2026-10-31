package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type PTZ interface {
	Step(context.Context, string, string, string, float64) error
	Stop(context.Context, string, string) error
	Preset(context.Context, string, string, string) error
}

type Handler struct {
	cfg      config.Config
	apiToken string
	ptz      PTZ
	locks    map[string]*sync.Mutex
}

type PublicCamera struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model,omitempty"`
	RTSPURL string `json:"rtsp_url"`
	PTZ     bool   `json:"ptz"`
	Mode    string `json:"mode"`
}

func New(cfg config.Config, token string, ptz PTZ) http.Handler {
	h := &Handler{cfg: cfg, apiToken: token, ptz: ptz, locks: map[string]*sync.Mutex{}}
	for _, camera := range cfg.Cameras {
		h.locks[camera.ID] = &sync.Mutex{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]any{"status": "configured", "cameras": len(cfg.Cameras)})
	})
	mux.HandleFunc("GET /v1/cameras", h.list)
	mux.HandleFunc("POST /v1/cameras/{id}/ptz/step", h.step)
	mux.HandleFunc("POST /v1/cameras/{id}/ptz/stop", h.stop)
	mux.HandleFunc("POST /v1/cameras/{id}/ptz/preset", h.preset)
	mux.HandleFunc("GET /v1/frigate/config", h.frigate)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
			auth := r.Header.Get("Authorization")
			actual := strings.TrimPrefix(auth, "Bearer ")
			if token == "" || !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare([]byte(actual), []byte(token)) != 1 {
				respond(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	cams := []PublicCamera{}
	for _, c := range h.cfg.Cameras {
		cams = append(cams, PublicCamera{ID: c.ID, Name: c.Name, Model: c.Model, RTSPURL: c.RTSPURL, PTZ: c.PTZ, Mode: "onvif-bridge"})
	}
	respond(w, 200, map[string]any{"version": "0.1.0", "cameras": cams})
}

func (h *Handler) lookup(w http.ResponseWriter, r *http.Request) (config.Camera, bool) {
	for _, c := range h.cfg.Cameras {
		if c.ID == r.PathValue("id") {
			if !c.PTZ {
				respond(w, 409, map[string]string{"error": "ptz_not_supported"})
				return c, false
			}
			return c, true
		}
	}
	respond(w, 404, map[string]string{"error": "camera_not_found"})
	return config.Camera{}, false
}

func decode(w http.ResponseWriter, r *http.Request, to any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(to); err != nil {
		respond(w, 400, map[string]string{"error": "invalid_json"})
		return false
	}
	return true
}

func (h *Handler) step(w http.ResponseWriter, r *http.Request) {
	cam, ok := h.lookup(w, r)
	if !ok {
		return
	}
	var req struct {
		Direction string `json:"direction"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Direction != "up" && req.Direction != "down" && req.Direction != "left" && req.Direction != "right" {
		respond(w, 400, map[string]string{"error": "invalid_direction"})
		return
	}
	h.locks[cam.ID].Lock()
	defer h.locks[cam.ID].Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.ptz.Step(ctx, cam.ONVIFURL, cam.Profile, req.Direction, cam.Step); err != nil {
		respond(w, 502, map[string]string{"error": "ptz_backend_failed"})
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) stop(w http.ResponseWriter, r *http.Request) {
	cam, ok := h.lookup(w, r)
	if !ok {
		return
	}
	h.locks[cam.ID].Lock()
	defer h.locks[cam.ID].Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.ptz.Stop(ctx, cam.ONVIFURL, cam.Profile); err != nil {
		respond(w, 502, map[string]string{"error": "ptz_backend_failed"})
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) preset(w http.ResponseWriter, r *http.Request) {
	cam, ok := h.lookup(w, r)
	if !ok {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Token == "" || len(req.Token) > 80 {
		respond(w, 400, map[string]string{"error": "invalid_preset"})
		return
	}
	h.locks[cam.ID].Lock()
	defer h.locks[cam.ID].Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := h.ptz.Preset(ctx, cam.ONVIFURL, cam.Profile, req.Token); err != nil {
		respond(w, 502, map[string]string{"error": "ptz_backend_failed"})
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func quoteYAML(s string) string { b, _ := json.Marshal(s); return string(b) }

func (h *Handler) frigate(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("# Merge into existing Frigate config, do not replace it.\n")
	b.WriteString("# RTSP source: Xiaomi bridge; Frigate restream: go2rtc\n")
	b.WriteString("go2rtc:\n  streams:\n")
	for _, cam := range h.cfg.Cameras {
		fmt.Fprintf(&b, "    %s:\n      - %s\n", cam.ID, quoteYAML(cam.RTSPURL))
	}
	b.WriteString("cameras:\n")
	for _, cam := range h.cfg.Cameras {
		fmt.Fprintf(&b, "  %s:\n    ffmpeg:\n      inputs:\n        - path: %s\n          input_args: preset-rtsp-restream\n          roles: [detect, record]\n", cam.ID, quoteYAML("rtsp://127.0.0.1:8554/"+cam.ID))
		if cam.PTZ {
			u, _ := url.Parse(cam.ONVIFURL)
			host := u.Hostname()
			port := u.Port()
			if port == "" {
				port = "80"
			}
			fmt.Fprintf(&b, "    onvif:\n      host: %s\n      port: %s\n      user: \"\"\n      password: \"\"\n", quoteYAML(host), port)
		}
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(b.String()))
}
