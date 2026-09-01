package main

import (
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/chargeghost/engine/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// TUI subcommand dispatch happens before anything else writes to
	// stdout and before server-mode flag parsing sees the argument.
	if len(os.Args) > 1 && os.Args[1] == "tui" {
		runTUI(os.Args[2:])
		return
	}

	cfgPath := config.DefaultConfigPath()

	logLevelFlag := ""
	for i, arg := range os.Args {
		if arg == "-log-level" && i+1 < len(os.Args) {
			logLevelFlag = os.Args[i+1]
		} else if strings.HasPrefix(arg, "-log-level=") {
			logLevelFlag = strings.TrimPrefix(arg, "-log-level=")
		}
	}
	if v, ok := os.LookupEnv("LOG_LEVEL"); ok && v != "" {
		logLevelFlag = v
	}

	home, _ := os.UserHomeDir()
	baseDir := filepath.Join(home, ".chargeghost")

	boot, err := StartBoot(cfgPath, baseDir, ":8080")
	if err != nil {
		os.Exit(1)
	}

	configureLogger(boot.Fleet.Config().LogMode)
	if logLevelFlag != "" {
		_ = configureLogger(logLevelFlag)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutdown signal received — stopping")
	boot.Shutdown()
}

func configureLogger(mode string) *slog.LevelVar {
	levelVar := &slog.LevelVar{}
	switch mode {
	case "debug":
		levelVar.Set(slog.LevelDebug)
	case "warn":
		levelVar.Set(slog.LevelWarn)
	case "error":
		levelVar.Set(slog.LevelError)
	default:
		levelVar.Set(slog.LevelInfo)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: levelVar})))
	return levelVar
}
