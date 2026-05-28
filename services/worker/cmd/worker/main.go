package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"go.uber.org/zap"

	"pz1.2/services/worker/internal/consumer"
	"pz1.2/shared/logger"
)

func main() {
	log := logger.New("worker")
	defer log.Sync()

	rabbitURL := os.Getenv("RABBIT_URL")
	if rabbitURL == "" {
		rabbitURL = "amqp://guest:guest@localhost:5672/"
	}

	queueName := os.Getenv("QUEUE_NAME")
	if queueName == "" {
		queueName = "task_events"
	}

	prefetch := 1
	if v := os.Getenv("WORKER_PREFETCH"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			prefetch = parsed
		}
	}

	consumer, err := consumer.New(rabbitURL, queueName, prefetch, log)
	if err != nil {
		log.Fatal("failed to create consumer", zap.Error(err))
	}
	defer consumer.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := consumer.Run(ctx); err != nil {
		log.Fatal("worker stopped with error", zap.Error(err))
	}
}
