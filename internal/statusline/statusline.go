package statusline

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Writer sends normal log lines and footer updates through an inline terminal UI.
type Writer struct {
	mu       sync.Mutex
	out      io.Writer
	enabled  bool
	started  bool
	program  *tea.Program
	done     chan error
	status   string
	progress string
}

type statusMessage string
type progressMessage string
type readyMessage struct{}

type model struct {
	status    string
	progress  string
	width     int
	ready     chan struct{}
	readySent bool
}

func newModel(ready chan struct{}) model {
	return model{ready: ready}
}

func (m model) Init() tea.Cmd {
	return func() tea.Msg { return readyMessage{} }
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case readyMessage:
		if m.ready != nil && !m.readySent {
			close(m.ready)
			m.readySent = true
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case statusMessage:
		m.status = string(msg)
	case progressMessage:
		m.progress = string(msg)
	case tea.InterruptMsg:
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
	if m.width == 0 || (m.status == "" && m.progress == "") {
		return ""
	}
	footer := lipgloss.NewStyle().Width(m.width).Align(lipgloss.Right)
	status := footer.Render("\x1b[1;36m" + m.status + "\x1b[0m")
	progress := footer.Render(m.progress)
	return lipgloss.JoinVertical(lipgloss.Left, status, progress)
}

// New creates a writer. Enable the TUI only for an interactive terminal.
func New(out io.Writer, enabled bool) *Writer {
	return &Writer{out: out, enabled: enabled}
}

// Start runs the inline footer and waits until it is ready for updates.
func (w *Writer) Start() error {
	if !w.enabled {
		return nil
	}

	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return errors.New("terminal UI already started")
	}

	ready := make(chan struct{})
	w.done = make(chan error, 1)
	w.program = tea.NewProgram(
		newModel(ready),
		tea.WithInput(os.Stdin),
		tea.WithOutput(w.out),
	)
	program := w.program
	done := w.done
	w.started = true
	w.mu.Unlock()

	go func() {
		_, err := program.Run()
		done <- err
	}()

	select {
	case <-ready:
		return nil
	case err := <-done:
		w.mu.Lock()
		w.started = false
		w.program = nil
		w.done = nil
		w.mu.Unlock()
		if err != nil {
			return fmt.Errorf("start terminal UI: %w", err)
		}
		return errors.New("terminal UI stopped before it was ready")
	}
}

// Logf prints a newline-terminated log line above the inline footer.
func (w *Writer) Logf(format string, args ...any) {
	w.LogfTo(w.out, format, args...)
}

// LogfTo prints the message normally or above the inline footer when active.
func (w *Writer) LogfTo(out io.Writer, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		w.program.Println(strings.TrimSuffix(text, "\n"))
		return
	}
	fmt.Fprint(out, text)
}

// Status updates the footer row above the progress bar.
func (w *Writer) Status(message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled || !w.started || w.status == message {
		return
	}
	w.status = message
	w.program.Send(statusMessage(message))
}

// Progress updates the footer's bottom row.
func (w *Writer) Progress(message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled || !w.started || w.progress == message {
		return
	}
	w.progress = message
	w.program.Send(progressMessage(message))
}

// FinishProgress clears the bar while leaving the operation status visible.
func (w *Writer) FinishProgress() {
	w.Progress("")
}

// Close exits the inline terminal UI.
func (w *Writer) Close() error {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return nil
	}
	w.started = false
	program := w.program
	done := w.done
	w.program = nil
	w.done = nil
	w.mu.Unlock()

	program.Quit()
	runErr := <-done
	if runErr != nil {
		return fmt.Errorf("run terminal UI: %w", runErr)
	}
	return nil
}

var defaultWriter = New(
	os.Stdout,
	term.IsTerminal(int(os.Stdout.Fd())) &&
		term.IsTerminal(int(os.Stdin.Fd())) &&
		term.IsTerminal(int(os.Stderr.Fd())),
)

// Start initializes the inline terminal UI when all standard streams are terminals.
func Start() error {
	return defaultWriter.Start()
}

// Logf writes ordinary log output.
func Logf(format string, args ...any) {
	defaultWriter.Logf(format, args...)
}

// LogfTo writes a line to the chosen stream.
func LogfTo(out io.Writer, format string, args ...any) {
	defaultWriter.LogfTo(out, format, args...)
}

// Status updates the shared operation label above the progress bar.
func Status(message string) {
	defaultWriter.Status(message)
}

// Progress updates the shared bottom progress row.
func Progress(message string) {
	defaultWriter.Progress(message)
}

// FinishProgress clears the shared progress row.
func FinishProgress() {
	defaultWriter.FinishProgress()
}

// Close shuts down the shared inline terminal UI.
func Close() error {
	return defaultWriter.Close()
}
