package controllers

import (
	"context"
	"net/http"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

//go:generate mockgen -package=controllers -source=manager_interface.go -destination=manager_mock.go Manager

// Manager defines the interface for a controller-runtime manager
type Manager interface {
	// Add adds a runnable to the Manager
	Add(manager.Runnable) error

	// AddHealthzCheck allows you to add a HealthzCheck to the Manager
	AddHealthzCheck(name string, check healthz.Checker) error

	// AddReadyzCheck allows you to add a ReadyzCheck to the Manager
	AddReadyzCheck(name string, check healthz.Checker) error

	// AddMetricsServerExtraHandler adds an extra handler to the Metrics server
	AddMetricsServerExtraHandler(path string, handler http.Handler) error

	// Start starts the Manager and waits for it to be stopped
	Start(ctx context.Context) error

	// GetConfig returns the rest.Config used by the Manager
	GetConfig() *rest.Config

	// GetScheme returns the scheme.Scheme used by the Manager
	GetScheme() *runtime.Scheme

	// GetClient returns the client.Client used by the Manager
	GetClient() client.Client

	// GetFieldIndexer returns the client.FieldIndexer used by the Manager
	GetFieldIndexer() client.FieldIndexer

	// GetCache returns the cache.Cache used by the Manager
	GetCache() cache.Cache

	// GetEventRecorderFor returns a new record.EventRecorder for the provided name
	GetEventRecorderFor(name string) record.EventRecorder

	// GetRESTMapper returns the meta.RESTMapper used by the Manager
	GetRESTMapper() meta.RESTMapper

	// GetAPIReader returns a client.Reader that will hit the API server
	GetAPIReader() client.Reader

	// GetWebhookServer returns the webhook server used by the Manager
	GetWebhookServer() webhook.Server

	// GetLogger returns the logger used by the Manager
	GetLogger() logr.Logger

	// GetControllerOptions returns the controller options
	GetControllerOptions() config.Controller

	// Elected returns a channel that is closed when this Manager is elected leader
	Elected() <-chan struct{}

	// GetHTTPClient returns the http.Client used by the Manager
	GetHTTPClient() *http.Client
}
