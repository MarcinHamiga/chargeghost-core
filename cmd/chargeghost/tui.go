package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/client"
	"github.com/chargeghost/engine/internal/config"
	"github.com/chargeghost/engine/internal/logging"
)

// runTUI boots the engine headless on a loopback port and drives it from a
// Bubble Tea program. Logging is redirected (file + ring buffer) before any
// engine component runs, because FleetManager logs during construction and
// the terminal is owned by the TUI from here on.
func runTUI(args []string) {
	listen := "127.0.0.1:0"
	logLevel := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--listen" || arg == "-listen":
			if i+1 < len(args) {
				i++
				listen = args[i]
			}
		case strings.HasPrefix(arg, "--listen="):
			listen = strings.TrimPrefix(arg, "--listen=")
		case strings.HasPrefix(arg, "-listen="):
			listen = strings.TrimPrefix(arg, "-listen=")
		case arg == "-log-level" || arg == "--log-level":
			if i+1 < len(args) {
				i++
				logLevel = args[i]
			}
		case strings.HasPrefix(arg, "-log-level="):
			logLevel = strings.TrimPrefix(arg, "-log-level=")
		case strings.HasPrefix(arg, "--log-level="):
			logLevel = strings.TrimPrefix(arg, "--log-level=")
		}
	}

	// Non-TTY guard: Bubble Tea needs an interactive terminal. Fail fast,
	// before any engine component starts.
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "chargeghost tui requires an interactive terminal")
		os.Exit(1)
	}

	home, _ := os.UserHomeDir()
	baseDir := filepath.Join(home, ".chargeghost")

	// Logging FIRST: FleetManager logs during construction, and those
	// entries must land in the file/ring rather than on the TUI's screen.
	dual, err := logging.NewDualHandler(filepath.Join(baseDir, "logs"), "tui.log", 1000)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chargeghost tui: cannot open log file:", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(dual))
	// The TUI owns the terminal: route chi's HTTP access log (a dedicated
	// stdout logger, not the std log package) into the TUI log file too.
	api.SetHTTPAccessLog(dual.Writer())
	if logLevel != "" {
		applyLogLevel(dual.Level(), logLevel)
	}

	boot, err := StartBoot(config.DefaultConfigPath(), baseDir, listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chargeghost tui: failed to start engine:", err)
		os.Exit(1)
	}

	cli := client.New("http://" + boot.Addr)
	events := cli.Subscribe("scope=all")

	p := tea.NewProgram(newPlaceholderApp(cli, events, boot.Addr), tea.WithAltScreen())
	_, runErr := p.Run()

	events.Stop()
	boot.Shutdown()

	// Terminal is ours again: restore a stderr logger for the final lines.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "chargeghost tui exited with error:", runErr)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "chargeghost tui — goodbye")
}

func applyLogLevel(levelVar interface{ Set(slog.Level) }, mode string) {
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
}

// --- Placeholder app (replaced by the real shell in phase 2) ---

type stationsLoadedMsg struct {
	count int
	err   error
}

type placeholderApp struct {
	cli      *client.Client
	events   *client.Events
	addr     string
	stations int
	loadErr  error
}

func newPlaceholderApp(cli *client.Client, events *client.Events, addr string) placeholderApp {
	return placeholderApp{cli: cli, events: events, addr: addr}
}

func (m placeholderApp) Init() tea.Cmd {
	return func() tea.Msg {
		stations, err := m.cli.ListStations()
		return stationsLoadedMsg{count: len(stations), err: err}
	}
}

func (m placeholderApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case stationsLoadedMsg:
		m.stations = msg.count
		m.loadErr = msg.err
	}
	return m, nil
}

func (m placeholderApp) View() string {
	if m.loadErr != nil {
		return fmt.Sprintf("chargeghost tui — API on http://%s — stations: error (%v) — [q] quit", m.addr, m.loadErr)
	}
	return fmt.Sprintf("chargeghost tui — API on http://%s — %d stations — [q] quit", m.addr, m.stations)
}
