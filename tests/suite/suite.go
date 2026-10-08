package suite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	grpcapp "grpc-auth/internal/app/grpc"
	"grpc-auth/internal/services/auth"
	"grpc-auth/internal/storage/sqlite"

	ssov1 "github.com/KaiserRed/protos/gen/go/sso"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	TokenTTL    = time.Hour
	testTimeout = 10 * time.Minute
)

type Suite struct {
	*testing.T
	AuthClient ssov1.AuthClient
}

var (
	grpcAddr string
	storage  *sqlite.Storage
	server   *grpcapp.App
	tmpDir   string
)

func Start() error {
	dir, err := os.MkdirTemp("", "sso-test-*")
	if err != nil {
		return err
	}
	tmpDir = dir

	dbPath := filepath.Join(dir, "test.db")

	if err := migrateUp(dbPath); err != nil {
		return err
	}

	if err := seedApp(dbPath); err != nil {
		return err
	}

	storage, err = sqlite.New(dbPath)
	if err != nil {
		return err
	}

	log := slog.New(slog.DiscardHandler)

	authService := auth.New(log, storage, storage, storage, TokenTTL)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	grpcAddr = l.Addr().String()

	server = grpcapp.New(log, authService, l.Addr().(*net.TCPAddr).Port)
	go func() {
		_ = server.Serve(l)
	}()

	return nil
}

func Stop() {
	if server != nil {
		server.Stop()
	}
	if storage != nil {
		_ = storage.Stop()
	}
	if tmpDir != "" {
		_ = os.RemoveAll(tmpDir)
	}
}

func New(t *testing.T) (context.Context, *Suite) {
	t.Helper()
	t.Parallel()

	ctx, cancelCtx := context.WithTimeout(context.Background(), testTimeout)

	t.Cleanup(func() {
		t.Helper()
		cancelCtx()
	})

	cc, err := grpc.NewClient(
		grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc server connection failed: %v", err)
	}

	t.Cleanup(func() {
		t.Helper()
		_ = cc.Close()
	})

	return ctx, &Suite{
		T:          t,
		AuthClient: ssov1.NewAuthClient(cc),
	}
}

func migrateUp(dbPath string) error {
	m, err := migrate.New(
		"file://"+filepath.Join("..", "migrations"),
		"sqlite3://"+dbPath,
	)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}

	return nil
}

func seedApp(dbPath string) error {
	seed, err := os.ReadFile(filepath.Join("migrations", "1_init_apps.up.sql"))
	if err != nil {
		return err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	_, err = db.Exec(string(seed))

	return err
}
