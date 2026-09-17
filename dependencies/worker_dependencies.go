package dependencies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"orderflow/app"
	"orderflow/infra/payment"
	"orderflow/infra/postgres"
	"orderflow/outbox"
	"orderflow/pkg/circuitbreaker"
	"orderflow/pkg/logger"
	"orderflow/pkg/metrics"
	"orderflow/pkg/requestid"
	"orderflow/services"
	"orderflow/worker"
)

const defaultWorkerCount = 4

type orderCreatedPayload struct {
	OrderID    string `json:"order_id"`
	TotalCents int64  `json:"total_cents"`
	RequestID  string `json:"request_id,omitempty"`
}

// WorkerDependencies holds what the worker entrypoint needs: the delegator,
// the pool, and the outbox dispatcher that feeds it.
type WorkerDependencies struct {
	App         *app.AppContext
	Delegator   *services.Delegator
	Pool        *worker.Pool
	Dispatcher  *outbox.Dispatcher
	metricsPort int
	workers     int
}

func NewWorkerDependencies(appCtx *app.AppContext) *WorkerDependencies {
	cfg := appCtx.Config

	count := cfg.Worker.Count
	if count <= 0 {
		count = defaultWorkerCount
	}

	orderRepo := postgres.NewOrderRepository(appCtx.DB)
	paymentRepo := postgres.NewPaymentRepository(appCtx.DB)
	outboxRepo := postgres.NewOutboxRepository(appCtx.DB)

	breaker := circuitbreaker.New(cfg.CircuitBreaker.FailureThreshold, time.Duration(cfg.CircuitBreaker.ResetTimeoutSeconds)*time.Second)
	gateway := payment.NewMockGateway(cfg.Payment.FailureRate, time.Duration(cfg.Payment.LatencyMs)*time.Millisecond)
	paymentService := services.NewPaymentService(gateway, breaker, paymentRepo, orderRepo)
	notificationService := services.NewNotificationService(appCtx.Logger)

	delegator := services.NewDelegator()
	delegator.Register("order.created", handleOrderCreated(appCtx, outboxRepo, paymentService, notificationService, breaker))

	pool := worker.NewPool(count, delegator)

	dispatcher := outbox.NewDispatcher(
		outboxRepo,
		pool,
		time.Duration(cfg.Outbox.PollIntervalSeconds)*time.Second,
		cfg.Outbox.BatchSize,
		appCtx.Logger,
	)

	return &WorkerDependencies{
		App:         appCtx,
		Delegator:   delegator,
		Pool:        pool,
		Dispatcher:  dispatcher,
		metricsPort: cfg.Metrics.WorkerPort,
		workers:     count,
	}
}

// handleOrderCreated is the business logic behind the "order.created"
// event: charge the order (through the circuit breaker), notify, then mark
// the outbox row processed/failed and record the breaker's state as a
// metric.
func handleOrderCreated(
	appCtx *app.AppContext,
	outboxRepo *postgres.OutboxRepository,
	paymentService *services.PaymentService,
	notificationService *services.NotificationService,
	breaker *circuitbreaker.Breaker,
) services.EventHandlerFunc {
	return func(ctx context.Context, job worker.Job) error {
		var payload orderCreatedPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			_ = outboxRepo.MarkFailed(ctx, job.ID)
			return fmt.Errorf("unmarshal order.created payload: %w", err)
		}

		// The request-id travels: HTTP request -> outbox payload -> here, so
		// this job's log lines carry the same id as the original API call.
		if payload.RequestID != "" {
			ctx = requestid.WithID(ctx, payload.RequestID)
		}
		log := logger.Ctx(ctx, appCtx.Logger)

		paymentErr := paymentService.ProcessPayment(ctx, payload.OrderID, payload.TotalCents)
		metrics.CircuitBreakerState.WithLabelValues("payment_gateway").Set(float64(breaker.State()))

		if paymentErr != nil {
			log.Warn("payment processing failed", "order_id", payload.OrderID, "err", paymentErr)
		}
		if err := notificationService.Notify(ctx, payload.OrderID, "processed"); err != nil {
			log.Error("notification failed", "order_id", payload.OrderID, "err", err)
		}

		result := "success"
		if paymentErr != nil {
			result = "payment_failed"
		}
		metrics.OutboxEventsProcessed.WithLabelValues(job.Type, result).Inc()

		return outboxRepo.MarkProcessed(ctx, job.ID)
	}
}

// Run starts the worker pool, the outbox dispatcher, and this process's own
// /metrics endpoint together, and blocks until all three have stopped
// after ctx is cancelled. The worker needs its own metrics server because
// it's a separate OS process from the API server — Prometheus's default
// registry is in-memory and per-process, so metrics recorded here (circuit
// breaker state, outbox events processed) are invisible to the server's
// /metrics unless this process exposes them itself.
func (w *WorkerDependencies) Run(ctx context.Context) error {
	w.App.Logger.Info("worker pool starting", "workers", w.workers)

	metricsSrv := &http.Server{
		Addr:    ":" + strconv.Itoa(w.metricsPort),
		Handler: promhttp.Handler(),
	}

	var wg sync.WaitGroup
	wg.Add(3)

	go func() { defer wg.Done(); _ = w.Pool.Run(ctx) }()
	go func() { defer wg.Done(); _ = w.Dispatcher.Run(ctx) }()
	go func() {
		defer wg.Done()
		w.App.Logger.Info("worker metrics listening", "addr", metricsSrv.Addr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			w.App.Logger.Error("worker metrics server failed", "err", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = metricsSrv.Shutdown(shutdownCtx)

	wg.Wait()
	return nil
}
