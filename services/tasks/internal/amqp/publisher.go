package amqp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"pz1.2/shared/events"
	"pz1.2/shared/middleware"
)

type Publisher struct {
	mu        sync.Mutex
	conn      *amqp091.Connection
	rabbitURL string
	queueName string
	producer  string
	version   string
	log       *zap.Logger
}

func NewPublisher(rabbitURL, queueName, producer, version string, log *zap.Logger) *Publisher {
	return &Publisher{
		rabbitURL: rabbitURL,
		queueName: queueName,
		producer:  producer,
		version:   version,
		log:       log.With(zap.String("component", "task_event_publisher")),
	}
}

func (p *Publisher) PublishTaskCreated(ctx context.Context, taskID string) error {
	conn, err := p.getConnection()
	if err != nil {
		return err
	}

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()

	if _, err := ch.QueueDeclare(
		p.queueName,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare queue: %w", err)
	}

	msg := events.TaskEvent{
		Event:     "task.created",
		TaskID:    taskID,
		TS:        time.Now().UTC().Format(time.RFC3339),
		RequestID: middleware.GetRequestID(ctx),
		Producer:  p.producer,
		Version:   p.version,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	if err := ch.PublishWithContext(
		ctx,
		"",
		p.queueName,
		false,
		false,
		amqp091.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp091.Persistent,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		},
	); err != nil {
		return fmt.Errorf("publish event: %w", err)
	}

	p.log.Info("task event published", zap.String("event", msg.Event), zap.String("task_id", taskID))
	return nil
}

func (p *Publisher) Close() error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return nil
	}

	err := p.conn.Close()
	p.conn = nil
	return err
}

func (p *Publisher) getConnection() (*amqp091.Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil && !p.conn.IsClosed() {
		return p.conn, nil
	}

	conn, err := amqp091.Dial(p.rabbitURL)
	if err != nil {
		return nil, fmt.Errorf("connect to rabbitmq: %w", err)
	}

	p.conn = conn
	p.log.Info("connected to rabbitmq", zap.String("queue", p.queueName))
	return p.conn, nil
}
