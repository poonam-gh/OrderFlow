package dependencies

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"orderflow/app"
	"orderflow/handlers"
	"orderflow/infra/postgres"
	orderflowredis "orderflow/infra/redis"
	"orderflow/routers"
	"orderflow/services"
)

// ServerDependencies holds what the HTTP entrypoint needs: the gin engine and routes.
type ServerDependencies struct {
	App    *app.AppContext
	Engine *gin.Engine
}

func NewServerDependencies(appCtx *app.AppContext) *ServerDependencies {
	engine := gin.Default() // Logger + Recovery middleware included

	orderRepo := postgres.NewOrderRepository(appCtx.DB)
	orderService := services.NewOrderService(orderRepo)
	idempotencyStore := orderflowredis.NewIdempotencyStore(appCtx.Redis, time.Duration(appCtx.Config.Idempotency.TTLHours)*time.Hour)
	orderHandler := handlers.NewOrderHandler(orderService, idempotencyStore)

	productRepo := postgres.NewProductRepository(appCtx.DB)
	productCache := orderflowredis.NewProductCache(appCtx.Redis, time.Duration(appCtx.Config.ProductCache.TTLSeconds)*time.Second)
	productService := services.NewProductService(productRepo, productCache)
	productHandler := handlers.NewProductHandler(productService)

	userRepo := postgres.NewUserRepository(appCtx.DB)
	tokenTTL := time.Duration(appCtx.Config.Auth.TokenTTLMinutes) * time.Minute
	userService := services.NewUserService(userRepo, appCtx.Config.Auth.JWTSecret, tokenTTL)
	userHandler := handlers.NewUserHandler(userService)

	routers.Register(engine, appCtx, orderHandler, productHandler, userHandler)

	return &ServerDependencies{App: appCtx, Engine: engine}
}

// Run shuts down gracefully on ctx cancellation, giving in-flight requests 10s to finish.
func (s *ServerDependencies) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Engine,
	}

	errCh := make(chan error, 1)
	go func() {
		s.App.Logger.Info("server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		s.App.Logger.Info("shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
