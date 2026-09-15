package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"orderflow/config"
	"orderflow/infra/postgres"
	redisinfra "orderflow/infra/redis"
)

// AppContext holds the dependencies every entrypoint needs: config, logger, DB, Redis.
type AppContext struct {
	Config *config.Config
	Logger *slog.Logger
	DB     *sqlx.DB
	Redis  *redis.Client
}

func NewAppContext(ctx context.Context, configPath string) (*AppContext, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	pool, err := postgres.NewPool(ctx, cfg.PostgresDSN())
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	rdb, err := redisinfra.NewClient(ctx, cfg.Redis.Addr)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	return &AppContext{
		Config: cfg,
		Logger: logger,
		DB:     pool,
		Redis:  rdb,
	}, nil
}

func (a *AppContext) Close() {
	a.DB.Close()
	_ = a.Redis.Close()
}
