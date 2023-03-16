package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	appclientset "github.com/kanisterio/kanister/pkg/client/clientset/versioned"
	"github.com/pmpplatform/pzap"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.uber.org/zap"
	"k8s.io/client-go/rest"
)

// Declare metrics
var (
	kanisterActionSets = prometheus.NewDesc("kanister_actionsets", "Kanister ActionSets status", []string{"name", "state", "error", "running", "progress"}, nil)
)

type kanisterCollector struct {
	ctx       context.Context
	appClient *appclientset.Clientset
}

func (c kanisterCollector) Describe(descs chan<- *prometheus.Desc) {
	descs <- kanisterActionSets
}

func (c kanisterCollector) Collect(metrics chan<- prometheus.Metric) {
	actionsets, err := c.appClient.CrV1alpha1().ActionSets("kanister").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		panic(err)
	}
	for item := range actionsets.Items {
		actionset := actionsets.Items[item]

		metrics <- prometheus.MustNewConstMetric(kanisterActionSets, prometheus.GaugeValue, 1,
			actionset.Name,
			string(actionset.Status.State),
			actionset.Status.Error.Message,
			actionset.Status.Progress.RunningPhase,
			actionset.Status.Progress.PercentCompleted,
		)
	}

}

func initCollector(c *rest.Config) *kanisterCollector {
	return &kanisterCollector{
		ctx:       context.Background(),
		appClient: appclientset.NewForConfigOrDie(c),
	}
}

func main() {
	defer pzap.MustZap()()

	// Listen on application shutdown signals.
	listener := make(chan os.Signal, 1)
	signal.Notify(listener, os.Interrupt, syscall.SIGTERM)

	config, err := rest.InClusterConfig()
	if err != nil {
		zap.S().Fatal(err)
	}

	collector := initCollector(config)
	r := prometheus.NewRegistry()
	r.MustRegister(collector)
	handler := promhttp.HandlerFor(r, promhttp.HandlerOpts{})

	mux := http.DefaultServeMux
	mux.Handle("/metrics", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	srv := &http.Server{
		Addr:         ":9090",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 1 * time.Minute,
		IdleTimeout:  15 * time.Second,
	}

	// Run server in background
	zap.S().Infof("Starting HTTP server on port 9090")
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			zap.S().Fatalf("HTTP server crashed %v", err)
		}
	}()

	// Handle shutdown
	<-listener

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		zap.S().Errorf("HTTP server graceful shutdown failed %v", err)
	} else {
		zap.S().Info("HTTP server stopped")
	}
}
