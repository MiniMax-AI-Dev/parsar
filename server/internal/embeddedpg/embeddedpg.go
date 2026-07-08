package embeddedpg

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	Port     = 15432
	User     = "parsar"
	Password = "parsar"
	Database = "parsar"
)

type EmbeddedPG struct {
	db       *embeddedpostgres.EmbeddedPostgres
	dataDir  string
	adopted  bool // true when we reused an orphan PG — don't call pg.Stop()
}

func connectionURL(dbName string) string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", User, Password, Port, dbName)
}

func ConnectionURL() string { return connectionURL(Database) }

// Start launches an embedded PostgreSQL instance under dataDir/postgres.
// If an orphan PG from a previous crash is already listening on the port,
// it is killed first so we get a clean startup with proper lifecycle control.
func Start(dataDir string) (*EmbeddedPG, error) {
	pgDir := filepath.Join(dataDir, "postgres")
	if err := os.MkdirAll(pgDir, 0o700); err != nil {
		return nil, fmt.Errorf("embeddedpg: mkdir %s: %w", pgDir, err)
	}

	if isPortListening(Port) {
		killOrphanPG(Port)
	}

	runtimeDir := filepath.Join(pgDir, "runtime")
	dbDataDir := filepath.Join(pgDir, "data")

	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Username(User).
			Password(Password).
			Database("postgres").
			Port(Port).
			DataPath(dbDataDir).
			RuntimePath(runtimeDir).
			BinariesPath(filepath.Join(pgDir, "bin")),
	)

	if err := pg.Start(); err != nil {
		return nil, fmt.Errorf("embeddedpg: start: %w", err)
	}

	if err := ensureDatabase(); err != nil {
		pg.Stop() //nolint:errcheck
		return nil, err
	}

	return &EmbeddedPG{db: pg, dataDir: pgDir}, nil
}

func (e *EmbeddedPG) Stop() error {
	if e.db == nil {
		return nil
	}
	return e.db.Stop()
}

func ensureDatabase() error {
	conn, err := sql.Open("pgx", connectionURL("postgres"))
	if err != nil {
		return fmt.Errorf("embeddedpg: open postgres db: %w", err)
	}
	defer conn.Close()

	var exists bool
	err = conn.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", Database).Scan(&exists)
	if err != nil {
		return fmt.Errorf("embeddedpg: check database: %w", err)
	}
	if exists {
		return nil
	}

	if _, err := conn.Exec("CREATE DATABASE " + Database); err != nil {
		return fmt.Errorf("embeddedpg: create database: %w", err)
	}
	return nil
}

func isPortListening(port uint32) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*1e6)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// killOrphanPG finds and kills any postgres process listening on the port.
// This handles the case where a previous `go run` was killed but left the
// PG child process alive.
func killOrphanPG(port uint32) {
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port)).Output()
	if err != nil || len(out) == 0 {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 1 {
			continue
		}
		if p, err := os.FindProcess(pid); err == nil {
			p.Signal(os.Interrupt)
		}
	}
	// Brief wait for the process to exit
	for range 20 {
		if !isPortListening(port) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
