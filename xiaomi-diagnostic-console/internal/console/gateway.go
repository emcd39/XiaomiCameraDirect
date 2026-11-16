package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const maxGatewayResponse = 2 << 20

type Gateway struct {
	cfg              Config
	client           *http.Client
	selectionMu      sync.Mutex
	mu               sync.RWMutex
	inventorySummary InventorySummary
	cameras          map[string]gatewayCamera
	selectedCameraID string
	inventoryFetched time.Time
}

type gatewayCamera struct {
	option     CameraOption
	model      string
	rtspURL    string
	videoState string
}

type CameraOption struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Model         string `json:"model"`
	Video         bool   `json:"video"`
	VideoReady    bool   `json:"video_ready"`
	VideoState    string `json:"video_state"`
	SessionActive bool   `json:"session_active"`
	Reason        string `json:"reason,omitempty"`
	PTZ           bool   `json:"ptz"`
	Selected      bool   `json:"selected"`
}

type InventorySummary struct {
	Loaded             bool           `json:"loaded"`
	CameraCount        int            `json:"camera_count"`
	C201Count          int            `json:"c201_count"`
	StreamURLsDetected int            `json:"stream_urls_detected"`
	Cameras            []CameraOption `json:"cameras"`
	C201Cameras        []CameraOption `json:"c201_cameras"`
	SelectedCameraID   string         `json:"selected_camera_id,omitempty"`
	SessionReady       bool           `json:"session_ready"`
	SessionActive      bool           `json:"session_active"`
	SessionState       string         `json:"session_state,omitempty"`
	FetchedAt          string         `json:"fetched_at,omitempty"`
}

type GatewayStatus struct {
	Reachable  bool             `json:"reachable"`
	HTTPStatus int              `json:"http_status"`
	InfoStatus int              `json:"info_status"`
	Mode       string           `json:"mode"`
	Inventory  InventorySummary `json:"inventory"`
	Note       string           `json:"note"`
}

type managedInventory struct {
	Cameras []managedInventoryCamera `json:"cameras"`
}

type managedInventoryCamera struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model"`
	ModelID string `json:"model_id"`
	Video   struct {
		State         string `json:"state"`
		Reason        string `json:"reason"`
		SessionActive bool   `json:"session_active"`
	} `json:"video"`
	Capabilities *struct {
		Video bool `json:"video"`
		PTZ   bool `json:"ptz"`
	} `json:"capabilities"`
	Streams []struct {
		ID       string `json:"id"`
		Protocol string `json:"protocol"`
		URL      string `json:"url"`
	} `json:"streams"`
}

func newGateway(c Config) *Gateway {
	return &Gateway{
		cfg:     c,
		cameras: make(map[string]gatewayCamera),
		client: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				Proxy: nil,
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					host, _, err := net.SplitHostPort(address)
					if err != nil || !isLoopback(host) {
						return nil, errors.New("gateway connection to non-loopback forbidden")
					}
					return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, address)
				},
			},
		},
	}
}

func (g *Gateway) token() (string, error) {
	if g.cfg.TokenFile == "" {
		return "", errors.New("token_file is not configured")
	}
	b, err := os.ReadFile(g.cfg.TokenFile)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("token_file is empty")
	}
	return token, nil
}

func (g *Gateway) request(ctx context.Context, endpoint string, auth bool) ([]byte, int, error) {
	return g.do(ctx, http.MethodGet, endpoint, nil, auth)
}

func (g *Gateway) do(ctx context.Context, method, endpoint string, body []byte, auth bool) ([]byte, int, error) {
	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.cfg.GatewayURL+endpoint, requestBody)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		tok, e := g.token()
		if e != nil {
			return nil, 0, e
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxGatewayResponse))
	return b, resp.StatusCode, err
}

func (g *Gateway) Status(ctx context.Context) GatewayStatus {
	g.mu.RLock()
	inv := g.inventorySummary
	inv.Cameras = append([]CameraOption(nil), inv.Cameras...)
	inv.C201Cameras = append([]CameraOption(nil), inv.C201Cameras...)
	g.mu.RUnlock()
	status := GatewayStatus{Mode: "live", Inventory: inv, Note: "Health, info and selected-camera metadata are read-only; a camera session is opened only by an explicit inventory or selection action"}
	_, code, err := g.request(ctx, "/healthz", false)
	status.HTTPStatus = code
	status.Reachable = err == nil && code == http.StatusOK
	_, infoCode, err2 := g.request(ctx, "/v2/info", true)
	if err2 == nil {
		status.InfoStatus = infoCode
	}
	g.refreshSelectedCameraState(ctx)
	status.Inventory = g.inventorySnapshot()
	return status
}

func (g *Gateway) refreshSelectedCameraState(ctx context.Context) {
	g.mu.RLock()
	cameraID := g.selectedCameraID
	expected, exists := g.cameras[cameraID]
	fetchedAt := g.inventoryFetched
	g.mu.RUnlock()
	if cameraID == "" || !exists || expected.rtspURL == "" {
		return
	}
	b, code, err := g.request(ctx, "/v2/cameras?include_streams=false", true)
	if err != nil || code != http.StatusOK {
		return
	}
	var document managedInventory
	if json.Unmarshal(b, &document) != nil {
		return
	}
	item, found := findManagedCamera(g, document, cameraID, expected.model)
	if found {
		g.mu.RLock()
		stillCurrent := g.selectedCameraID == cameraID && g.inventoryFetched.Equal(fetchedAt)
		g.mu.RUnlock()
		if !stillCurrent {
			return
		}
		g.setCameraSession(cameraID, item)
	}
}

// RefreshInventory fetches managed camera records and keeps capabilities and
// RTSP endpoints paired with the exact camera that advertised them.
func (g *Gateway) RefreshInventory(ctx context.Context) (InventorySummary, error) {
	// Inventory is metadata-only. Starting every advertised video source just
	// to populate the selector creates camera sessions the user did not choose.
	b, code, err := g.request(ctx, "/v2/cameras?include_streams=false", true)
	if err != nil {
		return InventorySummary{}, err
	}
	if code != http.StatusOK {
		return InventorySummary{}, fmt.Errorf("gateway inventory returned HTTP %d", code)
	}
	var document managedInventory
	if err := json.Unmarshal(b, &document); err != nil {
		return InventorySummary{}, fmt.Errorf("gateway inventory JSON: %w", err)
	}
	return g.applyInventory(document), nil
}

func (g *Gateway) applyInventory(document managedInventory) InventorySummary {
	now := time.Now()
	cameras := make(map[string]gatewayCamera)
	options := make([]CameraOption, 0, len(document.Cameras))
	c201Options := make([]CameraOption, 0)
	streams := 0
	for _, camera := range document.Cameras {
		if camera.ID == "" {
			continue
		}
		modelName := camera.Model
		if modelName == "" {
			modelName = camera.ModelID
		}
		isC201 := strings.EqualFold(modelName, "xiaomi.camera.c201")
		videoSupported, ptzSupported := false, false
		if camera.Capabilities != nil {
			videoSupported = camera.Capabilities.Video
			ptzSupported = camera.Capabilities.PTZ
		} else if isC201 {
			// Older gateway responses predate capability fields. Preserve the
			// existing verified C201 behavior, while keeping unknown models off.
			videoSupported, ptzSupported = true, true
		}
		videoState := strings.ToLower(strings.TrimSpace(camera.Video.State))
		if videoState == "" {
			videoState = "not_prepared"
		}
		reason := strings.TrimSpace(camera.Video.Reason)
		for _, stream := range camera.Streams {
			if (stream.Protocol == "" || stream.Protocol == "rtsp") && g.allowRTSP(stream.URL) {
				streams++
			}
		}
		name := strings.TrimSpace(camera.Name)
		if name == "" {
			name = modelName
			if name == "" {
				name = "Xiaomi camera"
			}
		}
		item := gatewayCamera{
			option: CameraOption{
				ID: camera.ID, Name: name, Model: modelName,
				Video: videoSupported, VideoState: videoState, Reason: reason,
				SessionActive: camera.Video.SessionActive, PTZ: ptzSupported,
			},
			model:      modelName,
			videoState: videoState,
		}
		if videoSupported {
			for _, stream := range camera.Streams {
				if (stream.Protocol != "" && stream.Protocol != "rtsp") || !g.allowRTSP(stream.URL) {
					continue
				}
				if item.rtspURL == "" || stream.ID == "live" || stream.ID == "main" {
					item.rtspURL = stream.URL
				}
				if stream.ID == "live" || stream.ID == "main" {
					break
				}
			}
		}
		item.option.VideoReady = videoSupported && videoState == "streaming" && item.rtspURL != ""
		if !videoSupported && reason == "" {
			reason = "unsupported_model"
			item.option.Reason = reason
		}
		if videoSupported && !item.option.VideoReady && reason == "" {
			item.option.Reason = videoState
		}
		cameras[camera.ID] = item
		options = append(options, item.option)
		if isC201 {
			c201Options = append(c201Options, item.option)
		}
	}

	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	g.mu.Lock()
	g.cameras = cameras
	g.inventoryFetched = now
	g.selectedCameraID = ""
	// Older gateways may return a usable source with the inventory even when
	// asked for metadata only. With current gateways the user selects the
	// camera explicitly before the console requests a source.
	if len(c201Options) == 1 && cameras[c201Options[0].ID].rtspURL != "" {
		g.selectedCameraID = c201Options[0].ID
	} else if len(options) == 1 && cameras[options[0].ID].rtspURL != "" {
		g.selectedCameraID = options[0].ID
	}
	for i := range options {
		options[i].Selected = options[i].ID == g.selectedCameraID
		item := cameras[options[i].ID]
		item.option = options[i]
		cameras[options[i].ID] = item
	}
	for i := range c201Options {
		c201Options[i].Selected = c201Options[i].ID == g.selectedCameraID
	}
	summary := InventorySummary{
		Loaded:             true,
		CameraCount:        len(options),
		C201Count:          len(c201Options),
		StreamURLsDetected: streams,
		Cameras:            options,
		C201Cameras:        c201Options,
		SelectedCameraID:   g.selectedCameraID,
		SessionState:       "not_prepared",
		FetchedAt:          now.UTC().Format(time.RFC3339),
	}
	if g.selectedCameraID != "" {
		item := cameras[g.selectedCameraID]
		summary.SessionReady = item.option.VideoReady
		summary.SessionActive = item.option.SessionActive
		summary.SessionState = item.videoState
	}
	g.inventorySummary = summary
	g.mu.Unlock()
	return summary
}

// SelectCamera pins a camera from the current managed inventory. The
// identifier must have come from that inventory response.
func (g *Gateway) SelectCamera(cameraID string) error {
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	item, ok := g.cameras[cameraID]
	if !ok {
		return errors.New("selected camera is not in the current inventory")
	}
	g.selectedCameraID = cameraID
	g.inventorySummary.SelectedCameraID = cameraID
	g.inventorySummary.SessionReady = item.option.VideoReady
	g.inventorySummary.SessionActive = item.option.SessionActive
	g.inventorySummary.SessionState = item.videoState
	for i := range g.inventorySummary.Cameras {
		g.inventorySummary.Cameras[i].Selected = g.inventorySummary.Cameras[i].ID == cameraID
	}
	for i := range g.inventorySummary.C201Cameras {
		g.inventorySummary.C201Cameras[i].Selected = g.inventorySummary.C201Cameras[i].ID == cameraID
	}
	return nil
}

func (g *Gateway) Camera(cameraID string) (CameraOption, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	item, ok := g.cameras[cameraID]
	return item.option, ok
}

func (g *Gateway) SelectedCameraID() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.selectedCameraID
}

func (g *Gateway) inventorySnapshot() InventorySummary {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := g.inventorySummary
	result.Cameras = append([]CameraOption(nil), result.Cameras...)
	result.C201Cameras = append([]CameraOption(nil), result.C201Cameras...)
	return result
}

func (g *Gateway) PrepareCamera(ctx context.Context, cameraID string) error {
	if cameraID == "" {
		return errors.New("select a camera from the inventory")
	}
	g.mu.RLock()
	item, exists := g.cameras[cameraID]
	selected := g.selectedCameraID
	g.mu.RUnlock()
	if !exists {
		return errors.New("selected camera is no longer available in the current inventory")
	}
	if !item.option.PTZ {
		return errors.New("PTZ is not supported for the selected camera")
	}
	if selected != cameraID {
		return errors.New("camera selection changed before PTZ step; no request was sent")
	}
	if err := ctx.Err(); err != nil {
		return errors.New("PTZ request expired before session check; no request was sent")
	}
	if !item.option.SessionActive {
		return errors.New("selected camera has no active camera session; no PTZ request was sent")
	}
	return nil
}

// StartCameraSession opens at most one managed source for an explicit
// selection action. It never repeats streams_for after an EOF.
func (g *Gateway) StartCameraSession(ctx context.Context, cameraID string) error {
	if cameraID == "" {
		return errors.New("select a camera from the inventory")
	}
	g.mu.RLock()
	selected := g.selectedCameraID
	item, exists := g.cameras[cameraID]
	prepared := exists && selected == cameraID && g.inventorySummary.SelectedCameraID == cameraID &&
		item.option.SessionActive && item.rtspURL != ""
	g.mu.RUnlock()
	if selected != cameraID {
		return errors.New("camera selection changed before session preparation; no request was sent")
	}
	if !exists {
		return errors.New("selected camera is no longer available in the current inventory")
	}
	if !item.option.Video {
		return errors.New("video is not supported for the selected camera")
	}
	if prepared {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return errors.New("camera session request expired before source start; no request was sent")
	}
	endpoint := "/v2/cameras?streams_for=" + url.QueryEscape(cameraID)
	b, code, err := g.request(ctx, endpoint, true)
	if err != nil {
		return fmt.Errorf("gateway camera session request failed: %w", err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("gateway camera session request returned HTTP %d", code)
	}
	var document managedInventory
	if err := json.Unmarshal(b, &document); err != nil {
		return fmt.Errorf("gateway camera session response is invalid: %w", err)
	}
	g.mu.RLock()
	expected, expectedExists := g.cameras[cameraID]
	g.mu.RUnlock()
	if !expectedExists {
		return errors.New("selected camera is no longer available in the current inventory")
	}
	item, found := findManagedCamera(g, document, cameraID, expected.model)
	if !found {
		return errors.New("selected camera identity changed or is missing from gateway inventory")
	}
	if !item.option.Video {
		return errors.New("gateway did not confirm video capability for the selected camera")
	}
	if item.rtspURL == "" {
		return errors.New("gateway did not provide an allowed RTSP stream for the selected camera")
	}
	g.setCameraSession(cameraID, item)
	return nil
}

func findManagedCamera(g *Gateway, document managedInventory, cameraID, expectedModel string) (gatewayCamera, bool) {
	for _, camera := range document.Cameras {
		if camera.ID != cameraID {
			continue
		}
		modelName := camera.Model
		if modelName == "" {
			modelName = camera.ModelID
		}
		if !strings.EqualFold(modelName, expectedModel) {
			return gatewayCamera{}, false
		}
		isC201 := strings.EqualFold(modelName, "xiaomi.camera.c201")
		videoSupported, ptzSupported := false, false
		if camera.Capabilities != nil {
			videoSupported = camera.Capabilities.Video
			ptzSupported = camera.Capabilities.PTZ
		} else if isC201 {
			videoSupported, ptzSupported = true, true
		}
		videoState := strings.ToLower(strings.TrimSpace(camera.Video.State))
		if videoState == "" {
			videoState = "not_prepared"
		}
		name := strings.TrimSpace(camera.Name)
		if name == "" {
			name = modelName
		}
		item := gatewayCamera{
			option: CameraOption{
				ID: camera.ID, Name: name, Model: modelName,
				Video: videoSupported, VideoState: videoState,
				SessionActive: camera.Video.SessionActive,
				Reason:        strings.TrimSpace(camera.Video.Reason), PTZ: ptzSupported,
			},
			model: modelName, videoState: videoState,
		}
		if videoSupported {
			for _, stream := range camera.Streams {
				if (stream.Protocol == "" || stream.Protocol == "rtsp") && g.allowRTSP(stream.URL) {
					item.rtspURL = stream.URL
					if stream.ID == "live" || stream.ID == "main" {
						break
					}
				}
			}
		}
		item.option.VideoReady = videoSupported && videoState == "streaming" && item.rtspURL != ""
		return item, true
	}
	return gatewayCamera{}, false
}

func (g *Gateway) setCameraSession(cameraID string, item gatewayCamera) {
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.selectedCameraID != cameraID {
		return
	}
	if _, exists := g.cameras[cameraID]; !exists {
		return
	}
	// Keep the selected source across metadata-only updates until the
	// gateway confirms its session has ended. A new explicit selection can
	// still provide a fresh URL even if its initial status says retrying.
	if item.rtspURL == "" && (item.videoState != "retrying" || item.option.SessionActive) {
		item.rtspURL = g.cameras[cameraID].rtspURL
	}
	g.cameras[cameraID] = item
	item.option.Selected = true
	item.option.VideoReady = item.option.Video && item.videoState == "streaming" && item.rtspURL != ""
	g.cameras[cameraID] = item
	for i := range g.inventorySummary.Cameras {
		if g.inventorySummary.Cameras[i].ID == cameraID {
			g.inventorySummary.Cameras[i] = item.option
		} else {
			g.inventorySummary.Cameras[i].Selected = false
		}
	}
	for i := range g.inventorySummary.C201Cameras {
		if g.inventorySummary.C201Cameras[i].ID == cameraID {
			g.inventorySummary.C201Cameras[i] = item.option
		} else {
			g.inventorySummary.C201Cameras[i].Selected = false
		}
	}
	g.inventorySummary.SessionReady = item.option.VideoReady
	g.inventorySummary.SessionActive = item.option.SessionActive
	g.inventorySummary.SessionState = item.videoState
	g.inventorySummary.SelectedCameraID = cameraID
}

func (g *Gateway) setSessionState(cameraID, state string, ready bool) {
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.selectedCameraID == cameraID {
		g.inventorySummary.SessionReady = ready
		g.inventorySummary.SessionState = state
	}
}

// Step sends one authenticated managed PTZ POST to the explicitly selected
// camera. The client has redirects disabled and deliberately performs no retry.
func (g *Gateway) Step(ctx context.Context, cameraID, direction string) (string, error) {
	if direction != "left" && direction != "right" && direction != "up" && direction != "down" {
		return "", errors.New("invalid PTZ direction")
	}
	// Keep selection and inventory mutations out until the authenticated write
	// completes. The camera checked below is therefore the camera receiving it.
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", errors.New("PTZ request expired before the gateway write; no request was sent")
	}
	g.mu.RLock()
	selected := g.selectedCameraID
	item, exists := g.cameras[cameraID]
	sessionActive := item.option.SessionActive && g.inventorySummary.SelectedCameraID == cameraID
	g.mu.RUnlock()
	if !exists {
		return "", errors.New("selected camera is not available in the current inventory")
	}
	if selected != cameraID {
		return "", errors.New("camera selection changed before PTZ step; no request was sent")
	}
	if !item.option.PTZ {
		return "", errors.New("PTZ is not supported for the selected camera")
	}
	if !sessionActive {
		return "", errors.New("selected camera has no active camera session")
	}
	body, err := json.Marshal(map[string]string{"direction": direction})
	if err != nil {
		return "", errors.New("could not encode PTZ request")
	}
	endpoint := "/v2/cameras/" + url.PathEscape(cameraID) + "/ptz/step"
	response, code, err := g.do(ctx, http.MethodPost, endpoint, body, true)
	if err != nil {
		return "", fmt.Errorf("gateway PTZ request failed and was not retried: %w", err)
	}
	if code != http.StatusOK {
		return "", gatewayAPIError(code, response)
	}
	var result struct {
		WriteStatus string `json:"write_status"`
	}
	if err := json.Unmarshal(response, &result); err != nil || result.WriteStatus != "sent" {
		return "", errors.New("gateway returned an invalid PTZ result")
	}
	return result.WriteStatus, nil
}

func gatewayAPIError(status int, body []byte) error {
	var response struct {
		Error string `json:"error"`
	}
	code := ""
	if json.Unmarshal(body, &response) == nil && len(response.Error) > 0 && len(response.Error) <= 64 {
		valid := true
		for _, ch := range response.Error {
			if !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') && ch != '_' {
				valid = false
				break
			}
		}
		if valid {
			code = response.Error
		}
	}
	if code != "" {
		return fmt.Errorf("gateway PTZ API returned HTTP %d (%s)", status, code)
	}
	return fmt.Errorf("gateway PTZ API returned HTTP %d", status)
}

func (g *Gateway) allowRTSP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "rtsp" || u.Hostname() == "" || u.User != nil {
		return false
	}
	host := u.Hostname()
	if isLoopback(host) {
		return true
	}
	for _, allowed := range g.cfg.AllowedRTSPHosts {
		if strings.EqualFold(host, allowed) {
			return true
		}
	}
	return false
}

func (g *Gateway) RTSPURL() (string, error) {
	g.mu.RLock()
	cameraID := g.selectedCameraID
	item, exists := g.cameras[cameraID]
	ready := cameraID != "" && exists && g.inventorySummary.SelectedCameraID == cameraID &&
		g.inventorySummary.SessionReady && item.option.VideoReady
	raw := item.rtspURL
	g.mu.RUnlock()
	if !ready {
		return "", errors.New("selected camera has no ready managed RTSP stream")
	}
	if !g.allowRTSP(raw) {
		return "", errors.New("selected camera RTSP URL host is not allowed or URL includes credentials")
	}
	return raw, nil
}
