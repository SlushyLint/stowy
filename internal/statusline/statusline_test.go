package statusline

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelRendersStatusAboveProgressInTwoInlineRows(t *testing.T) {
	m := newModel(nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 8})
	m = updated.(model)

	for _, msg := range []tea.Msg{statusMessage("Downloading"), progressMessage("PROGRESS")} {
		updated, _ = m.Update(msg)
		m = updated.(model)
	}

	view := m.View()
	status := strings.Index(view, "Downloading")
	progress := strings.Index(view, "PROGRESS")
	if status < 0 || progress <= status {
		t.Fatalf("footer is not status above progress: %q", view)
	}
	if strings.Count(view, "\n") != 1 {
		t.Fatalf("inline footer has %d rows, want 2: %q", strings.Count(view, "\n")+1, view)
	}
}

func TestNonInteractiveWriterPrintsNewlineLogs(t *testing.T) {
	var out bytes.Buffer
	writer := New(&out, false)

	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	writer.Status("Downloading")
	writer.Progress("BAR")
	writer.Logf("regular output")
	writer.FinishProgress()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if got, want := out.String(), "regular output\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
