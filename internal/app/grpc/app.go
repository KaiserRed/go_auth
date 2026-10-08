package grpcapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"regexp"

	authgrpc "grpc-auth/internal/grpc/auth"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type App struct {
	log        *slog.Logger
	gRPCServer *grpc.Server
	port       int
}

func New(log *slog.Logger, authService authgrpc.Auth, port int) *App {
	loggingOpts := []logging.Option{
		logging.WithLogOnEvents(
			logging.StartCall, logging.FinishCall,
			logging.PayloadReceived, logging.PayloadSent,
		),
	}

	recoveryOpts := []recovery.Option{
		recovery.WithRecoveryHandler(func(p any) error {
			log.Error("recovered from panic", slog.Any("panic", p))
			return status.Errorf(codes.Internal, "internal error")
		}),
	}

	gRPCServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		recovery.UnaryServerInterceptor(recoveryOpts...),
		logging.UnaryServerInterceptor(InterceptorLogger(log), loggingOpts...),
	))

	authgrpc.Register(gRPCServer, authService)
	return &App{
		log:        log,
		gRPCServer: gRPCServer,
		port:       port,
	}
}

// InterceptorLogger adapts *slog.Logger to the logging.Logger interface
// expected by the logging interceptor.
func InterceptorLogger(l *slog.Logger) logging.Logger {
	return logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, msg string, fields ...any) {
		l.Log(ctx, slog.Level(lvl), msg, maskSecrets(fields)...)
	})
}

// secretFieldsRe matches a secret field value both in protobuf text format
// (password:"...", token:"...") and in JSON ("password":"...", "token":"...").
var secretFieldsRe = regexp.MustCompile(`(?i)("?(?:password|token)"?\s*[:=]\s*")[^"]*(")`)

// maskSecrets returns a copy of fields where payload values
// (grpc.request.content / grpc.response.content) are stripped of secrets.
// The interceptor passes fields as key/value pairs.
func maskSecrets(fields []any) []any {
	out := make([]any, len(fields))
	copy(out, fields)

	for i := 1; i < len(fields); i += 2 {
		key, ok := fields[i-1].(string)
		if !ok || (key != "grpc.request.content" && key != "grpc.response.content") {
			continue
		}

		switch v := fields[i].(type) {
		case string:
			out[i] = secretFieldsRe.ReplaceAllString(v, `${1}***${2}`)
		case fmt.Stringer:
			out[i] = secretFieldsRe.ReplaceAllString(v.String(), `${1}***${2}`)
		}
	}

	return out
}

func (a *App) MustRun() {
	if err := a.Run(); err != nil {
		panic(err)
	}
}

func (a *App) Run() error {
	const op = "grpcapp.Run"

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", a.port))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return a.Serve(l)
}

func (a *App) Serve(l net.Listener) error {
	const op = "grpcapp.Serve"

	a.log.With(
		slog.String("op", op),
		slog.Int("port", a.port),
	).Info("starting gRPC server", slog.String("addr", l.Addr().String()))

	if err := a.gRPCServer.Serve(l); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func (a *App) Stop() {
	const op = "grpcapp.Stop"

	a.log.With(slog.String("op", op)).Info("stopping gRPC server", slog.Int("port", a.port))
	a.gRPCServer.GracefulStop()
}
