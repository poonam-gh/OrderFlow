package dependencies

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"orderflow/app"
	"orderflow/handlers"
	"orderflow/infra/postgres"
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
	orderHandler := handlers.NewOrderHandler(orderService)

	routers.Register(engine, appCtx, orderHandler)

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
