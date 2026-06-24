package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/config"
	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/developerapi"
	"github.com/Ruleshift/server/internal/gatewayv2"
	"github.com/Ruleshift/server/internal/module"
	"github.com/Ruleshift/server/internal/roomcore"
	"github.com/Ruleshift/server/internal/runtimeclient"
	schedulerkube "github.com/Ruleshift/server/internal/scheduler/kubernetes"
	storagepostgres "github.com/Ruleshift/server/internal/storage/postgres"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		fatal("load config", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if cfg.DatabaseURL == "" {
		fatal("start gateway", errors.New("RULESHIFT_DATABASE_URL is required by protocol v2"))
	}
	platform, err := storagepostgres.Open(ctx, storagepostgres.Config{ControlURL: cfg.DatabaseURL, AdminURL: cfg.DatabaseAdminURL, ModuleDatabasePrefix: cfg.ModuleDatabasePrefix, DeveloperID: cfg.DeveloperID, DeveloperName: cfg.DeveloperName})
	if err != nil {
		fatal("open platform database", err)
	}
	defer platform.Close()
	if cfg.DeveloperAPIKey != "" {
		if err = platform.EnsureDeveloperAPIKey(ctx, cfg.DeveloperID, "bootstrap", cfg.DeveloperAPIKey); err != nil {
			fatal("bootstrap developer API key", err)
		}
	}
	kubeConfig, err := loadKubeConfig(cfg.KubeconfigPath)
	if err != nil {
		fatal("load Kubernetes config", err)
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		fatal("create Kubernetes client", err)
	}
	scheduler, err := schedulerkube.New(kubeClient)
	if err != nil {
		fatal("create module scheduler", err)
	}
	control := platform.V2ControlStore()
	endpointSource := moduleEndpointSource{control: control, scheduler: scheduler}
	resolver := runtimeclient.NewResolver(endpointSource)
	defer resolver.Close()
	guardedResolver := controlplane.TrackingResolver{Resolver: resolver, Tracker: controlplane.NewProtocolViolationTracker(control)}
	roomStore := platform.RoomCoreStore()
	rooms, err := roomcore.NewRegistry(roomStore, guardedResolver, cfg.RoomInputQueueSize)
	if err != nil {
		fatal("create room registry", err)
	}
	defer rooms.Close()
	authProvider := auth.NewPersistingProvider(auth.NewMockProvider(), platform)
	gateway, err := gatewayv2.New(gatewayv2.Config{MaxMessageBytes: cfg.MaxMessageBytes, SessionSendQueueSize: cfg.SessionSendQueueSize, AuthTimeout: cfg.AuthTimeout}, authProvider, rooms, logger)
	if err != nil {
		fatal("create gateway", err)
	}
	defer gateway.Close()
	validator := controlplane.Validator{Store: control, Scheduler: scheduler, Connector: runtimeclient.Connector{}, Runner: controlplane.DefaultConformanceRunner{}, Migrations: platform.AdditiveMigrationApplier()}
	roomManager := developerapi.RoomManager{Control: control, Rooms: rooms, Routes: roomStore, DatabaseName: platform.V2ModuleDatabaseName}
	developerAPI, err := developerapi.NewV2(platform, control, scheduler, validator, roomManager)
	if err != nil {
		fatal("create developer API v2", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", readyHandler)
	if cfg.EnableMetrics {
		mux.HandleFunc("/metrics", metricsHandler(rooms))
	}
	mux.Handle("/v2/developer/", developerAPI)
	mux.Handle("/v2/rooms", developerAPI)
	mux.Handle("/v2/rooms/", developerAPI)
	mux.HandleFunc(gatewayv2.WebSocketPath, gateway.HandleWebSocket)
	server := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: cfg.ReadTimeout, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout}
	go func() {
		<-ctx.Done()
		gateway.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("starting Ruleshift protocol v2 gateway", "addr", cfg.Addr)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("gateway stopped", err)
	}
}

type moduleEndpointSource struct {
	control   controlplane.Store
	scheduler *schedulerkube.Scheduler
}

func (s moduleEndpointSource) Endpoint(ctx context.Context, ref module.ModuleRef) (runtimeclient.Endpoint, error) {
	version, err := s.control.GetVersion(ctx, ref.DeveloperID, ref.ModuleID, ref.Version)
	if err != nil {
		return runtimeclient.Endpoint{}, err
	}
	if version.Ref.ImageDigest != ref.ImageDigest {
		return runtimeclient.Endpoint{}, fmt.Errorf("pinned image digest mismatch")
	}
	deployment, err := s.scheduler.ResolveDeployment(ctx, ref)
	if err != nil {
		return runtimeclient.Endpoint{}, err
	}
	deadline := time.Duration(version.Manifest.TransitionDeadlineMS) * time.Millisecond
	return runtimeclient.Endpoint{Address: deployment.Endpoint, Token: deployment.RPCToken, StateTypeURL: version.Manifest.StateTypeURL, CommandTypeURLs: version.Manifest.CommandTypeURLs, TransitionDeadline: deadline}, nil
}
func loadKubeConfig(path string) (*rest.Config, error) {
	if path != "" {
		return clientcmd.BuildConfigFromFlags("", path)
	}
	return rest.InClusterConfig()
}
func fatal(message string, err error) { slog.Error(message, "error", err); os.Exit(1) }
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
func readyHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}
func metricsHandler(registry interface{ RoomCount() int }) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "ruleshift_up 1\nruleshift_rooms %d\n", registry.RoomCount())
	}
}
