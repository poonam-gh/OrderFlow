package services

import (
	"context"
	"log/slog"

	"orderflow/pkg/logger"
)

// NotificationService is a stand-in for a real notifier (email/SMS/push) —
// it logs, so the outbox → worker → notification chain is observable
// without needing a real provider.
type NotificationService struct {
	logger *slog.Logger
}

func NewNotificationService(logger *slog.Logger) *NotificationService {
	return &NotificationService{logger: logger}
}

func (s *NotificationService) Notify(ctx context.Context, orderID, status string) error {
	logger.Ctx(ctx, s.logger).Info("notification sent", "order_id", orderID, "status", status)
	return nil
}
