package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`

	Postgres struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		DBName   string `yaml:"dbname"`
		SSLMode  string `yaml:"sslmode"`
	} `yaml:"postgres"`

	Redis struct {
		Addr string `yaml:"addr"`
	} `yaml:"redis"`

	Worker struct {
		Count int `yaml:"count"`
	} `yaml:"worker"`

	Auth struct {
		JWTSecret       string `yaml:"jwt_secret"`
		TokenTTLMinutes int    `yaml:"token_ttl_minutes"`
	} `yaml:"auth"`

	Payment struct {
		FailureRate float64 `yaml:"failure_rate"`
		LatencyMs   int     `yaml:"latency_ms"`
	} `yaml:"payment"`

	CircuitBreaker struct {
		FailureThreshold    int `yaml:"failure_threshold"`
		ResetTimeoutSeconds int `yaml:"reset_timeout_seconds"`
	} `yaml:"circuit_breaker"`

	Outbox struct {
		PollIntervalSeconds int `yaml:"poll_interval_seconds"`
		BatchSize           int `yaml:"batch_size"`
	} `yaml:"outbox"`

	RateLimit struct {
		Requests      int `yaml:"requests"`
		WindowSeconds int `yaml:"window_seconds"`
	} `yaml:"rate_limit"`

	ProductCache struct {
		TTLSeconds int `yaml:"ttl_seconds"`
	} `yaml:"product_cache"`

	Metrics struct {
		WorkerPort int `yaml:"worker_port"`
	} `yaml:"metrics"`

	Idempotency struct {
		TTLHours int `yaml:"ttl_hours"`
	} `yaml:"idempotency"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	return &cfg, nil
}

func (c *Config) PostgresDSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.Postgres.User,
		c.Postgres.Password,
		c.Postgres.Host,
		c.Postgres.Port,
		c.Postgres.DBName,
		c.Postgres.SSLMode,
	)
}
