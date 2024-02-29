package pkg

import (
	"context"
	"crypto/tls"
	"fmt"
	"runtime"
	"time"

	"github.com/wneessen/go-mail"
	"github.com/wneessen/go-mail/log"
	"golang.org/x/exp/slog"
)

type goMailLogConnector struct {
	logger *slog.Logger
}

func (g *goMailLogConnector) handle(level slog.Level, l log.Log) {
	ctx := context.Background()

	if !g.logger.Enabled(ctx, level) {
		return
	}

	var pcs [1]uintptr

	runtime.Callers(3, pcs[:])

	r := slog.NewRecord(time.Now(), level, fmt.Sprintf(l.Format, l.Messages...), pcs[0])

	switch l.Direction {
	case log.DirClientToServer:
		r.AddAttrs(
			slog.String(log.DirFromString, "client"),
			slog.String(log.DirToString, "server"),
		)
	case log.DirServerToClient:
		r.AddAttrs(
			slog.String(log.DirFromString, "server"),
			slog.String(log.DirToString, "client"),
		)
	}

	_ = g.logger.Handler().Handle(ctx, r)
}

func (g *goMailLogConnector) Errorf(l log.Log) {
	g.handle(slog.LevelError, l)
}

func (g *goMailLogConnector) Warnf(l log.Log) {
	g.handle(slog.LevelWarn, l)
}

func (g *goMailLogConnector) Infof(l log.Log) {
	g.handle(slog.LevelInfo, l)
}

func (g *goMailLogConnector) Debugf(l log.Log) {
	g.handle(slog.LevelDebug, l)
}

func newGoMailLogConnector(logger *slog.Logger) *goMailLogConnector {
	return &goMailLogConnector{logger: logger}
}

type Emailinator interface {
	TestConnection(ctx context.Context) error
	Send(ctx context.Context, msg *mail.Msg) error
	WithConfig(cfg *EmailConfig) Emailinator
	NewMsg() (*mail.Msg, error)
}

type emailinator struct {
	cfg    *EmailConfig
	logger *slog.Logger
}

func (e *emailinator) newClient() (*mail.Client, error) {
	if e.cfg == nil {
		return nil, fmt.Errorf("unable to create email client, no config set")
	}

	var authOption mail.Option

	switch e.cfg.Port {
	case 587:
		authOption = mail.WithTLSConfig(&tls.Config{
			ServerName: e.cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
	case 465:
		authOption = mail.WithSSLPort(false)
	default:
		return nil, fmt.Errorf("unrecognized port: %d", e.cfg.Port)
	}

	c, err := mail.NewClient(
		e.cfg.Host,
		mail.WithPort(e.cfg.Port),
		mail.WithUsername(e.cfg.Username),
		mail.WithPassword(e.cfg.Password),
		mail.WithLogger(newGoMailLogConnector(e.logger)),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
		authOption,
	)

	if err != nil {
		return nil, fmt.Errorf("unable to create email client: %w", err)
	}

	c.SetDebugLog(e.logger.Enabled(context.Background(), slog.LevelDebug))

	return c, nil
}

func (e *emailinator) TestConnection(ctx context.Context) error {
	client, err := e.newClient()
	if err != nil {
		return err
	}

	defer client.Close()

	return client.DialWithContext(ctx)
}

func (e *emailinator) Send(ctx context.Context, msg *mail.Msg) error {
	client, err := e.newClient()
	if err != nil {
		return err
	}

	defer client.Close()

	return client.DialAndSendWithContext(ctx, msg)
}

func (e *emailinator) NewMsg() (*mail.Msg, error) {
	m := mail.NewMsg()

	if err := m.From(e.cfg.Username); err != nil {
		return nil, fmt.Errorf("unable to set from address to %s: %w", e.cfg.Username, err)
	}

	return m, nil
}

func (e *emailinator) WithConfig(cfg *EmailConfig) Emailinator {
	return &emailinator{
		cfg:    cfg,
		logger: e.logger,
	}
}

func NewEmailinator(logger *slog.Logger) Emailinator {
	return &emailinator{
		logger: logger,
	}
}

type MockEmailinator struct {
	TestConnectionError error
	SendError           error
	NewMsgError         error
}

func (m MockEmailinator) TestConnection(ctx context.Context) error {
	return m.TestConnectionError
}

func (m MockEmailinator) Send(ctx context.Context, msg *mail.Msg) error {
	return m.SendError
}

func (m MockEmailinator) NewMsg() (*mail.Msg, error) {
	return mail.NewMsg(), m.NewMsgError
}

func (m MockEmailinator) WithConfig(cfg *EmailConfig) Emailinator {
	return m
}

func NewMockEmailinator() *MockEmailinator {
	return &MockEmailinator{
		TestConnectionError: nil,
	}
}
