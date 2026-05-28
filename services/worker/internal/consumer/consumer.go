package consumer

import (
	"context"
	"encoding/json"
	"fmt"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"pz1.2/shared/events"
)

type Consumer struct {
	conn      *amqp091.Connection
	queueName string
	prefetch  int
	log       *zap.Logger
}

func New(rabbitURL, queueName string, prefetch int, log *zap.Logger) (*Consumer, error) {
	if prefetch <= 0 {
		prefetch = 1
	}

	conn, err := amqp091.Dial(rabbitURL)
	if err != nil {
		return nil, fmt.Errorf("connect to rabbitmq: %w", err)
	}

	return &Consumer{
		conn:      conn,
		queueName: queueName,
		prefetch:  prefetch,
		log:       log.With(zap.String("component", "worker_consumer")),
	}, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()

	if _, err := ch.QueueDeclare(
		c.queueName,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare queue: %w", err)
	}

	if err := ch.Qos(c.prefetch, 0, false); err != nil {
		return fmt.Errorf("configure qos: %w", err)
	}

	msgs, err := ch.Consume(
		c.queueName,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	c.log.Info("worker started", zap.String("queue", c.queueName), zap.Int("prefetch", c.prefetch))

	for {
		select {
		case <-ctx.Done():
			c.log.Info("worker stopping")
			return nil
		case d, ok := <-msgs:
			if !ok {
				return fmt.Errorf("delivery channel closed")
			}

			var ev events.TaskEvent
			if err := json.Unmarshal(d.Body, &ev); err != nil {
				c.log.Warn("bad message", zap.Error(err))
				if nackErr := d.Nack(false, false); nackErr != nil {
					c.log.Warn("nack failed", zap.Error(nackErr))
				}
				continue
			}

			c.log.Info(
				"task event received",
				zap.String("event", ev.Event),
				zap.String("task_id", ev.TaskID),
				zap.String("ts", ev.TS),
				zap.String("request_id", ev.RequestID),
				zap.String("producer", ev.Producer),
				zap.String("version", ev.Version),
			)

			if err := d.Ack(false); err != nil {
				c.log.Warn("ack failed", zap.Error(err))
			}
		}
	}
}

func (c *Consumer) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
