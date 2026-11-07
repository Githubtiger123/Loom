// Package mq 是 RabbitMQ 的最小封装：一个连接、一个 channel、一个 topic exchange。
//
// 一期默认关闭（mq.enabled=false）：状态同步和定时任务都跑在 cron/sync 进程里，
// 没有必须走消息队列的场景。等出现"异步通知、批量削峰、跨服务事件"再打开。
//
// 注意：amqp091 的 Connection/Channel 不是并发安全的（Channel 尤其），
// 真要并发发布时每个 goroutine 一个 channel，或在这里加一把锁 + 重连。
package mq

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Config 是连接与 exchange 参数。
type Config struct {
	URL      string
	Exchange string
	Prefetch int
}

// Client 是极简生产者封装。
type Client struct {
	conn     *amqp.Connection
	ch       *amqp.Channel
	exchange string
}

// Open 连接 broker，声明 topic exchange。
func Open(cfg Config) (*Client, error) {
	conn, err := amqp.DialConfig(cfg.URL, amqp.Config{
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
	})
	if err != nil {
		return nil, fmt.Errorf("连接 RabbitMQ 失败: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("打开 channel 失败: %w", err)
	}

	if cfg.Prefetch > 0 {
		if err := ch.Qos(cfg.Prefetch, 0, false); err != nil {
			_ = ch.Close()
			_ = conn.Close()
			return nil, fmt.Errorf("设置 prefetch 失败: %w", err)
		}
	}

	if cfg.Exchange != "" {
		// durable=true：broker 重启后 exchange 还在
		if err := ch.ExchangeDeclare(cfg.Exchange, "topic", true, false, false, false, nil); err != nil {
			_ = ch.Close()
			_ = conn.Close()
			return nil, fmt.Errorf("声明 exchange %q 失败: %w", cfg.Exchange, err)
		}
	}

	return &Client{conn: conn, ch: ch, exchange: cfg.Exchange}, nil
}

// Publish 发送一条持久化消息。
func (c *Client) Publish(ctx context.Context, routingKey string, body []byte) error {
	return c.ch.PublishWithContext(ctx, c.exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         body,
	})
}

// Health 供 /readyz 使用。连接已关闭说明需要重连。
func (c *Client) Health() error {
	if c.conn == nil || c.conn.IsClosed() {
		return fmt.Errorf("rabbitmq 连接已关闭")
	}
	return nil
}

// Close 依次关闭 channel 与连接。
func (c *Client) Close() error {
	if c.ch != nil {
		_ = c.ch.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
