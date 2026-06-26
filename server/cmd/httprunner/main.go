package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/httprunner"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Bg().Error("http runner failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	interval := flag.Duration("interval", 0, "wait duration between claimed HTTP agent runs")
	maxRuns := flag.Int("max-runs", 1, "maximum run-once attempts before exiting")
	once := flag.Bool("once", false, "run one attempt and exit")
	flag.Parse()
	if *once {
		*maxRuns = 1
		*interval = 0
	}
	if *interval < 0 {
		*interval = 0
	}
	if *maxRuns <= 0 {
		*maxRuns = 1
	}

	databaseURL := os.Getenv("DATABASE_URL")
	pool, err := db.OpenPool(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	runtimeStore := store.New(pool)

	result, err := httprunner.RunLoop(ctx, runtimeStore, nil, httprunner.LoopOptions{Interval: *interval, MaxRuns: *maxRuns})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := writeStatus(encoded); err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func writeStatus(encoded []byte) error {
	statusPath := os.Getenv("PARSAR_HTTP_RUNNER_STATUS")
	if statusPath == "" {
		statusPath = filepath.Join(os.Getenv("HOME"), ".parsar", "state", "http-runner-status.json")
	}
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(statusPath, append(encoded, '\n'), 0o644)
}

func init() {
	logDir := filepath.Join(os.Getenv("HOME"), ".parsar", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	logPath := os.Getenv("PARSAR_HTTP_RUNNER_LOG")
	if logPath == "" {
		logPath = filepath.Join(logDir, "http-runner.log")
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	// Route through the project's unified logger so http-runner gets the
	// same trace_id semantics as the server process. MultiWriter so
	// operators can tail the file AND see stderr when run interactively.
	log.Init(log.Config{
		Format: "json",
		Level:  slog.LevelInfo,
		Out:    io.MultiWriter(os.Stderr, file),
	})
}
