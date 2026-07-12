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

	"notification-service/internal/api"
	"notification-service/internal/channel"
	"notification-service/internal/config"
	"notification-service/internal/dispatcher"
	"notification-service/internal/domains"
	"notification-service/internal/hub"
	"notification-service/internal/queue"
	"notification-service/internal/retry"
	"notification-service/internal/store"
	"notification-service/internal/tracker"
)

func main() {
	// ── Logger ──────────────────────────────────────────────────────────────
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// ── Config ───────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Info("config loaded", "port", cfg.Port)

	// ── Signal context (graceful shutdown) ────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Database ─────────────────────────────────────────────────────────────
	if err := store.Migrate(cfg.DBUrl, "migrations"); err != nil {
		logger.Error("migrations failed", "error", err)
		os.Exit(1)
	}
	logger.Info("migrations applied")

	db, err := store.Connect(ctx, cfg.DBUrl)
	if err != nil {
		logger.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("database connected")

	// ── Stores ────────────────────────────────────────────────────────────────
	notifStore := store.NewNotificationStore(db)
	deliveryStore := store.NewDeliveryStore(db)
	prefStore := store.NewPreferenceStore(db)

	// ── SQS (main queue + DLQ) ────────────────────────────────────────────────
	q, err := queue.New(ctx, cfg.AWSEndpoint, cfg.AWSRegion, cfg.QueueURL)
	if err != nil {
		logger.Error("failed to create SQS queue client", "error", err)
		os.Exit(1)
	}
	dlq, err := queue.New(ctx, cfg.AWSEndpoint, cfg.AWSRegion, cfg.DLQUrl)
	if err != nil {
		logger.Error("failed to create SQS DLQ client", "error", err)
		os.Exit(1)
	}

	// ── AWS Channel Senders ───────────────────────────────────────────────────
	emailSender, err := channel.NewEmailSender(ctx, cfg.AWSEndpoint, cfg.AWSRegion, cfg.FromEmail)
	if err != nil {
		logger.Error("failed to create email sender", "error", err)
		os.Exit(1)
	}
	smsSender, err := channel.NewSMSSender(ctx, cfg.AWSEndpoint, cfg.AWSRegion)
	if err != nil {
		logger.Error("failed to create SMS sender", "error", err)
		os.Exit(1)
	}

	// ── WebSocket Hub ─────────────────────────────────────────────────────────
	wsHub := hub.New(logger)
	go wsHub.Run(ctx)

	wsSender := channel.NewWSSender(wsHub)

	// ── Tracker ───────────────────────────────────────────────────────────────
	t := tracker.New(notifStore, deliveryStore, logger)

	// ── Dispatcher + Worker Pool ──────────────────────────────────────────────
	d := dispatcher.New(emailSender, smsSender, wsSender, prefStore, t, logger)
	pool := dispatcher.NewWorkerPool(d, cfg.WorkersCount, logger)
	go pool.Run(ctx)

	// ── SQS Consumer ──────────────────────────────────────────────────────────
	// The SQS handler submits received notifications to the worker pool.
	// Returning nil deletes the message; returning an error leaves it visible
	// for SQS redelivery.
	consumer := queue.NewConsumer(q, func(ctx context.Context, n domains.Notification) error {
		pool.Submit(n)
		return nil
	}, logger)
	go func() {
		if err := consumer.Run(ctx); err != nil {
			logger.Error("SQS consumer error", "error", err)
		}
	}()

	// ── Retry Worker ──────────────────────────────────────────────────────────
	retryWorker := retry.New(
		deliveryStore, notifStore, prefStore,
		emailSender, smsSender, wsSender,
		t, dlq, logger,
	)
	go retryWorker.Run(ctx)

	// ── HTTP Server ───────────────────────────────────────────────────────────
	apiHandler := api.NewHandler(notifStore, prefStore, t, q, wsHub, logger)
	router := api.NewRouter(apiHandler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("HTTP server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
		}
	}()

	// ── Wait for shutdown signal ──────────────────────────────────────────────
	<-ctx.Done()
	logger.Info("shutdown signal received — draining in-flight requests")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}
	logger.Info("shutdown complete")
}
