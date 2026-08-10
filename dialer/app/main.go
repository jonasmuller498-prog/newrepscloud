package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("dialer stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	config, err := LoadConfig()
	if err != nil {
		return err
	}
	protector, err := NewProtector(
		config.PhoneHashKey, config.FieldEncryptionKey, config.AuditHMACKey)
	if err != nil {
		return err
	}
	root, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(root, 30*time.Second)
	defer cancel()
	store, err := openStore(startup, config, protector)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = runMigrations(startup, store.pool); err != nil {
		return err
	}
	gate := &DependencyGate{}
	gate.mediaReady.Store(store.MediaReady(startup))
	metrics := &Metrics{}
	ari := NewARIClient(config)
	journal := NewEventJournal(config.EventJournalDir)
	scheduler := &Scheduler{store, gate, metrics, log}
	originator := &Originator{
		store: store, config: config, client: ari, gate: gate, metrics: metrics, log: log,
	}
	processor := &ARIEventProcessor{
		store: store, client: ari, journal: journal, gate: gate, metrics: metrics, log: log,
	}
	consumer := &ARIConsumer{
		store: store, client: ari, processor: processor, config: config, gate: gate, log: log,
	}
	reconciler := &Reconciler{store, ari, gate, log}
	importer := &ImportWorker{store: store, log: log}
	var workers sync.WaitGroup
	startWorker := func(run func(context.Context)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			run(root)
		}()
	}
	startWorker(scheduler.Run)
	startWorker(originator.Run)
	startWorker(consumer.Run)
	startWorker(reconciler.Run)
	startWorker(importer.Run)
	publicServer := &http.Server{
		Addr: config.HTTPAddr, Handler: NewAPI(store, config, gate, metrics),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}
	metricsServer := &http.Server{
		Addr: config.MetricsAddr, Handler: NewMetricsAPI(store, gate, metrics),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	result := make(chan error, 2)
	startServer := func(name string, server *http.Server) {
		go func() {
			log.Info(name+" server started", "address", server.Addr)
			result <- server.ListenAndServe()
		}()
	}
	startServer("public HTTP", publicServer)
	startServer("metrics", metricsServer)
	select {
	case <-root.Done():
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
	}
	stop()
	shutdown, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	publicErr := publicServer.Shutdown(shutdown)
	metricsErr := metricsServer.Shutdown(shutdown)
	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()
	var workerErr error
	select {
	case <-workersDone:
	case <-shutdown.Done():
		workerErr = shutdown.Err()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return errors.Join(publicErr, metricsErr, workerErr)
}
