package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/backend"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/httpapi"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/onvif"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/store"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi"
)

type configuredPTZProvider struct {
	client  *onvif.Client
	cameras map[model.CameraID]config.Camera
}

func newConfiguredPTZProvider(client *onvif.Client, configured []config.Camera) *configuredPTZProvider {
	byID := make(map[model.CameraID]config.Camera, len(configured))
	for _, camera := range configured {
		byID[model.CameraID(camera.ID)] = camera
	}
	return &configuredPTZProvider{client: client, cameras: byID}
}

func (p *configuredPTZProvider) camera(id model.CameraID) (config.Camera, error) {
	camera, ok := p.cameras[id]
	if !ok || !camera.PTZ {
		return config.Camera{}, errors.New("PTZ is unsupported for this camera")
	}
	return camera, nil
}

func (p *configuredPTZProvider) Step(ctx context.Context, id model.CameraID, direction string) error {
	camera, err := p.camera(id)
	if err != nil {
		return err
	}
	return p.client.Step(ctx, camera.ONVIFURL, camera.Profile, direction, camera.Step)
}

func (p *configuredPTZProvider) Stop(ctx context.Context, id model.CameraID) error {
	camera, err := p.camera(id)
	if err != nil {
		return err
	}
	return p.client.Stop(ctx, camera.ONVIFURL, camera.Profile)
}

func (p *configuredPTZProvider) Preset(ctx context.Context, id model.CameraID, token string) error {
	camera, err := p.camera(id)
	if err != nil {
		return err
	}
	return p.client.Preset(ctx, camera.ONVIFURL, camera.Profile, token)
}

func main() {
	path := flag.String("config", "config.json", "Gateway configuration file")
	managed := flag.Bool("managed", false, "Start an empty managed-mode runtime")
	enableDiagnostics := flag.Bool("enable-diagnostics", false, "Enable the authenticated metadata-only camera session diagnostics route")
	listen := flag.String("listen", "127.0.0.1:8099", "Managed-mode HTTP listener")
	dataDir := flag.String("data-dir", "/data", "Managed-mode persistent data directory")
	check := flag.Bool("check-config", false, "Validate and exit")
	flag.Parse()
	cfg, err := loadConfig(*path, *managed, *listen)
	if err != nil {
		log.Fatal(err)
	}
	if *check {
		fmt.Printf("valid config: %d cameras\n", len(cfg.Cameras))
		return
	}
	token := os.Getenv("XLG_API_TOKEN")
	if token == "" {
		log.Fatal("XLG_API_TOKEN is required")
	}
	if !config.Loopback(cfg.Listen) && len(token) < 24 {
		log.Fatal("XLG_API_TOKEN must be >=24 characters on LAN listeners")
	}
	ptzClient := onvif.New()
	var cameras backend.CameraProvider
	var xiaomiProvider *xiaomi.Provider
	if *managed {
		xiaomiConfig, configured, err := xiaomi.ConfigFromEnv()
		if err != nil {
			log.Fatal(xiaomi.ErrorText(err))
		}
		if configured {
			xiaomiProvider, err = xiaomi.New(xiaomiConfig)
			if err != nil {
				log.Fatal(xiaomi.ErrorText(err))
			}
			cameras = xiaomiProvider
			defer xiaomiProvider.Close()
		}
	}
	handler, err := newHandlerWithDiagnostics(cfg, token, *managed, *dataDir, ptzClient, *enableDiagnostics, cameras)
	if err != nil {
		log.Fatalf("create gateway handler: %v", err)
	}
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 45 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		timeout, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = server.Shutdown(timeout)
	}()
	log.Printf("Xiaomi Local Gateway listening on %s, managed=%t, static_cameras=%d, xiaomi_provider=%t", cfg.Listen, *managed, len(cfg.Cameras), xiaomiProvider != nil)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func loadConfig(configPath string, managed bool, listen string) (config.Config, error) {
	if !managed {
		return config.Load(configPath)
	}
	if err := config.ValidateListen(listen); err != nil {
		return config.Config{}, err
	}
	return config.Config{Listen: listen}, nil
}

func openManagedStore(dataDir string) (*store.Store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("managed data directory is required")
	}
	return store.Open(filepath.Join(dataDir, "managed-state.json"))
}

func newHandler(cfg config.Config, token string, managed bool, dataDir string, ptzClient *onvif.Client, cameraProviders ...backend.CameraProvider) (http.Handler, error) {
	return newHandlerWithDiagnostics(cfg, token, managed, dataDir, ptzClient, false, cameraProviders...)
}

func newHandlerWithDiagnostics(cfg config.Config, token string, managed bool, dataDir string, ptzClient *onvif.Client, enableDiagnostics bool, cameraProviders ...backend.CameraProvider) (http.Handler, error) {
	if managed {
		state, err := openManagedStore(dataDir)
		if err != nil {
			return nil, fmt.Errorf("open managed state: %w", err)
		}
		var cameras backend.CameraProvider
		if len(cameraProviders) > 0 {
			cameras = cameraProviders[0]
		}
		return httpapi.NewWithManagedStateAndDiagnostics(cfg, token, ptzClient, cameras, nil, state, enableDiagnostics), nil
	}
	provider := backend.NewConfigProvider(cfg.Cameras)
	return httpapi.NewWithProviders(cfg, token, ptzClient, provider, newConfiguredPTZProvider(ptzClient, cfg.Cameras)), nil
}
