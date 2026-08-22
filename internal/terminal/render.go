// Package terminal renders timer state for interactive and redirected output.
package terminal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/onlinealarmkur/timer-cli/internal/countdown"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
	"github.com/onlinealarmkur/timer-cli/internal/recordcadence"
)

const (
	hideCursor          = "\x1b[?25l"
	showCursor          = "\x1b[?25h"
	clearLine           = "\r\x1b[2K"
	clearScreen         = "\x1b[H\x1b[2J"
	cycleNoticeDuration = time.Second
)

// Keep ambiguous-width characters at one cell. CJK and emoji grapheme clusters
// are still measured at their standard terminal widths.
var terminalWidth = &runewidth.Condition{StrictEmojiNeutral: true}

// Options controls output capabilities and presentation.
type Options struct {
	TTY bool
	// RawTerminal means output shares the terminal currently in raw keyboard mode.
	RawTerminal bool
	Fullscreen  bool
	ASCII       bool
	Quiet       bool
	FinalOnly   bool
	JSON        bool
	Loop        bool
	Controls    bool
	Title       string
	Language    localize.Language
	Size        func() (width, height int)
}

type frameMode uint8

const (
	modeCompactOne frameMode = iota + 1
	modeCompactTwo
	modeFullscreen
)

// logicalFrame contains only printable text and line feeds. Terminal control
// sequences are added separately when the frame is written.
type logicalFrame struct {
	body string
	mode frameMode
	rows int
}

type redirectedRecord struct {
	cadence time.Duration
	bucket  int64
	paused  bool
}

// Renderer safely tracks cursor state across all exit paths.
type Renderer struct {
	w            io.Writer
	opts         Options
	title        string
	cursorHidden bool
	closed       bool
	hasFrame     bool
	lastFrame    logicalFrame
	lastWidth    int
	lastHeight   int
	// lastPhysicalRows is the physical row count of lastFrame at the width it
	// was drawn (lastWidth). It equals lastFrame.rows at draw time because
	// draw-time content is truncated to fit that width, but it is
	// recomputed against the current width — see rowsToClear — when the
	// terminal has since narrowed, so a redraw/Finish/Close clear covers
	// rows the frame now occupies after rewrapping.
	lastPhysicalRows int
	hasRecord        bool
	lastRecord       redirectedRecord
	cycleNotice      string
	noticeUntil      time.Time
}

// exactWriter turns invalid short writes into errors for every renderer output
// path, including fmt and json helpers that otherwise trust the Writer contract.
type exactWriter struct{ io.Writer }

func (w exactWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

// New returns a renderer for the supplied output stream.
func New(w io.Writer, opts Options) *Renderer {
	if opts.Size == nil {
		opts.Size = func() (int, int) { return 80, 24 }
	}
	return &Renderer{w: exactWriter{Writer: w}, opts: opts, title: sanitizeHuman(opts.Title)}
}

func (r *Renderer) writeLine(payload string) error {
	lineEnding := "\n"
	if r.opts.RawTerminal {
		lineEnding = "\r\n"
	}
	_, err := io.WriteString(r.w, payload+lineEnding)
	return err
}

// Render draws current state. JSON, quiet, and final-only modes never emit a tick.
func (r *Renderer) Render(s countdown.Snapshot) error {
	if r.opts.JSON || r.opts.Quiet || r.opts.FinalOnly {
		return nil
	}
	if !r.opts.TTY {
		record := redirectedRecordFor(s)
		if r.hasRecord && record == r.lastRecord {
			return nil
		}
		state := "running"
		if s.Paused {
			state = "paused"
		}
		err := r.writeLine(fmt.Sprintf("remaining=%s progress=%d%% state=%s", FormatDuration(s.Remaining), percentage(s.Progress), state))
		if err == nil {
			r.hasRecord = true
			r.lastRecord = record
		}
		return err
	}

	width, height := r.opts.Size()
	width = max(0, width)
	height = max(0, height)
	frame := r.frameFor(s, width, height)
	if r.hasFrame && frame == r.lastFrame && width == r.lastWidth && height == r.lastHeight {
		return nil
	}
	if err := r.ensureCursorHidden(); err != nil {
		return err
	}

	var out strings.Builder
	switch {
	case frame.mode == modeFullscreen || (r.hasFrame && r.lastFrame.mode == modeFullscreen):
		out.WriteString(clearScreen)
	case r.hasFrame:
		out.WriteString(clearOwnedRows(r.rowsToClear(width)))
	default:
		out.WriteString(clearLine)
	}
	out.WriteString(terminalText(frame.body))
	if _, err := io.WriteString(r.w, out.String()); err != nil {
		return err
	}
	r.hasFrame = true
	r.lastFrame = frame
	r.lastWidth = width
	r.lastHeight = height
	r.lastPhysicalRows = physicalRows(frame.body, width)
	return nil
}

func redirectedRecordFor(s countdown.Snapshot) redirectedRecord {
	return redirectedRecord{
		cadence: recordcadence.Duration(s.Total),
		bucket:  recordcadence.Bucket(s.Total, s.Remaining),
		paused:  s.Paused,
	}
}

// Finish writes exactly one terminal or JSON result.
func (r *Renderer) Finish(s countdown.Snapshot, status, message string, at time.Time) error {
	if r.opts.JSON {
		result := struct {
			Status                 string  `json:"status"`
			DurationSeconds        float64 `json:"duration_seconds"`
			InitialDurationSeconds float64 `json:"initial_duration_seconds"`
			ElapsedSeconds         float64 `json:"elapsed_seconds"`
			Title                  string  `json:"title,omitempty"`
			Message                string  `json:"message,omitempty"`
			FinishedAt             string  `json:"finished_at"`
		}{
			Status: status, DurationSeconds: s.Total.Seconds(), InitialDurationSeconds: s.Initial.Seconds(),
			ElapsedSeconds: s.Elapsed.Seconds(), Title: r.opts.Title, Message: message,
			FinishedAt: at.UTC().Format(time.RFC3339),
		}
		payload, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return r.writeLine(string(payload))
	}
	if r.opts.Quiet {
		return nil
	}
	if r.opts.TTY {
		wasFullscreen := r.hasFrame && r.lastFrame.mode == modeFullscreen
		if status == "completed" && s.Finished && !r.opts.Loop && r.hasFrame && !wasFullscreen {
			if _, err := fmt.Fprintf(r.w, "\r\n%s\r\n", sanitizeHuman(message)); err != nil {
				return err
			}
			r.hasFrame = false
			r.cycleNotice = ""
			r.noticeUntil = time.Time{}
			return nil
		}
		clear := clearLine
		if r.hasFrame {
			if wasFullscreen {
				clear = clearScreen
			} else {
				width, _ := r.opts.Size()
				clear = clearOwnedRows(r.rowsToClear(max(0, width)))
			}
		}
		if _, err := io.WriteString(r.w, clear); err != nil {
			return err
		}
		r.hasFrame = false
		if status == "completed" && wasFullscreen {
			r.cycleNotice = sanitizeHuman(message)
			r.noticeUntil = at.Add(cycleNoticeDuration)
		} else {
			r.cycleNotice = ""
			r.noticeUntil = time.Time{}
		}
	}
	if status == "completed" {
		if r.opts.TTY {
			_, err := fmt.Fprintf(r.w, "%s\r\n", sanitizeHuman(message))
			return err
		}
		return r.writeLine(sanitizeHuman(message))
	}
	if status == "canceled" && r.opts.FinalOnly {
		return r.writeLine(localize.Text(r.opts.Language, localize.TimerCanceled))
	}
	return nil
}

// Close clears an owned frame and restores a hidden cursor. It is safe to call repeatedly.
func (r *Renderer) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true

	var clearErr error
	if r.hasFrame {
		clear := clearScreen
		if r.lastFrame.mode != modeFullscreen {
			width, _ := r.opts.Size()
			clear = clearOwnedRows(r.rowsToClear(max(0, width)))
		}
		_, clearErr = io.WriteString(r.w, clear)
		if clearErr == nil {
			r.hasFrame = false
		}
	}

	var cursorErr error
	if r.cursorHidden {
		_, cursorErr = io.WriteString(r.w, showCursor)
		if cursorErr == nil {
			r.cursorHidden = false
		}
	}
	return errors.Join(clearErr, cursorErr)
}

func (r *Renderer) frameFor(s countdown.Snapshot, width, height int) logicalFrame {
	if r.opts.Fullscreen {
		if frame, ok := r.fullscreenFrame(s, width, height); ok {
			return frame
		}
	}
	return r.compactFrame(s, width, height)
}

func (r *Renderer) compactFrame(s countdown.Snapshot, width, height int) logicalFrame {
	if width < 32 || height < 2 {
		return logicalFrame{body: fallbackLine(r.title, s, width, r.opts.Language), mode: modeCompactOne, rows: 1}
	}
	header := headerLine(r.title, s, width, r.opts.Language)
	progress := progressLine(s, width, r.opts.ASCII)
	return logicalFrame{body: header + "\n" + progress, mode: modeCompactTwo, rows: 2}
}

func (r *Renderer) fullscreenFrame(s countdown.Snapshot, width, height int) (logicalFrame, bool) {
	if width < 24 || height < 10 {
		return logicalFrame{}, false
	}
	digits := largeDigits(FormatDuration(s.Remaining))
	for _, line := range digits {
		if cellWidth(line) > width {
			return logicalFrame{}, false
		}
	}

	notice := ""
	if r.cycleNotice != "" && s.ObservedAt.Before(r.noticeUntil) {
		notice = centerCells(truncateCells(r.cycleNotice, width), width)
	} else if r.cycleNotice != "" {
		r.cycleNotice = ""
		r.noticeUntil = time.Time{}
	}
	lines := []string{centerCells(headerLine(r.title, s, width, r.opts.Language), width), notice}
	for _, line := range digits {
		lines = append(lines, centerCells(line, width))
	}
	lines = append(lines, "", centerCells(progressLine(s, width, r.opts.ASCII), width))
	if s.Paused {
		lines = append(lines, centerCells(localize.Text(r.opts.Language, localize.Paused), width))
	}
	if r.opts.Controls {
		lines = append(lines, centerCells(truncateCells(localize.Text(r.opts.Language, localize.Controls), width), width))
	}
	if len(lines) > height {
		return logicalFrame{}, false
	}
	return logicalFrame{body: strings.Join(lines, "\n"), mode: modeFullscreen, rows: len(lines)}, true
}

func (r *Renderer) ensureCursorHidden() error {
	if r.cursorHidden {
		return nil
	}
	if _, err := io.WriteString(r.w, hideCursor); err != nil {
		return err
	}
	r.cursorHidden = true
	return nil
}

// physicalRows returns how many physical terminal rows body occupies when
// drawn at width: each newline-separated line wraps across
// ceil(cellWidth(line) / width) rows, at least one. A non-positive width
// cannot wrap anything, so every line — including an empty body's single
// empty line — counts as exactly one row, matching clearOwnedRows's "a
// written frame occupies at least the cursor row" semantics.
func physicalRows(body string, width int) int {
	rows := 0
	for _, line := range strings.Split(body, "\n") {
		if width <= 0 {
			rows++
			continue
		}
		rows += max(1, int(math.Ceil(float64(cellWidth(line))/float64(width))))
	}
	return rows
}

// rowsToClear returns the physical row count to pass to clearOwnedRows for
// the last written frame. If the terminal has narrowed since that frame was
// drawn (currentWidth < r.lastWidth), the frame's lines may now wrap into
// more physical rows than lastPhysicalRows reflects, so the clear escalates
// to cover them; otherwise it returns lastPhysicalRows unchanged, which
// keeps clearing byte-identical to before this escalation existed.
func (r *Renderer) rowsToClear(currentWidth int) int {
	rows := r.lastPhysicalRows
	if currentWidth < r.lastWidth {
		if physical := physicalRows(r.lastFrame.body, currentWidth); physical > rows {
			rows = physical
		}
	}
	return rows
}

func clearOwnedRows(rows int) string {
	if rows <= 0 {
		return ""
	}
	var out strings.Builder
	out.WriteByte('\r')
	out.WriteString("\x1b[2K")
	for range rows - 1 {
		out.WriteString("\x1b[1A\x1b[2K")
	}
	out.WriteByte('\r')
	return out.String()
}

func terminalText(frame string) string {
	return strings.ReplaceAll(frame, "\n", "\r\n")
}

// FormatDuration rounds up so the display never shows zero before completion.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int64(math.Ceil(d.Seconds()))
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	seconds %= 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func percentage(progress float64) int {
	progress = max(0, min(1, progress))
	return int(math.Floor(progress * 100))
}

func progressBar(progress float64, width int, ascii bool) string {
	progress = max(0, min(1, progress))
	filled := int(progress * float64(width))
	fill, empty := "█", "░"
	if ascii {
		fill, empty = "#", "-"
	}
	return strings.Repeat(fill, filled) + strings.Repeat(empty, width-filled)
}

func progressLine(s countdown.Snapshot, width int, ascii bool) string {
	percent := fmt.Sprintf("  %d%%", percentage(s.Progress))
	barWidth := min(28, max(0, width-cellWidth(percent)))
	return progressBar(s.Progress, barWidth, ascii) + percent
}

func headerLine(title string, s countdown.Snapshot, width int, language localize.Language) string {
	if width <= 0 {
		return ""
	}
	remaining := localize.Format(language, localize.RemainingFormat, FormatDuration(s.Remaining))
	if s.Paused {
		remaining += " · " + localize.Text(language, localize.Paused)
		if line, ok := titleAndSuffix(title, remaining, width); ok {
			return line
		}
		return truncateCells(remaining, width)
	}
	full := localize.Format(language, localize.EndsAtFormat, s.Target.Format("15:04"), remaining)
	if line, ok := titleAndSuffix(title, full, width); ok {
		return line
	}
	if cellWidth(full) <= width {
		return full
	}
	if line, ok := titleAndSuffix(title, remaining, width); ok {
		return line
	}
	return truncateCells(remaining, width)
}

func titleAndSuffix(title, suffix string, width int) (string, bool) {
	if title == "" {
		return "", false
	}
	const separator = " · "
	titleWidth := width - cellWidth(separator) - cellWidth(suffix)
	if titleWidth < 1 {
		return "", false
	}
	return truncateCells(title, titleWidth) + separator + suffix, true
}

func fallbackLine(title string, s countdown.Snapshot, width int, language localize.Language) string {
	if width <= 0 {
		return ""
	}
	suffix := fmt.Sprintf("%s %d%%", FormatDuration(s.Remaining), percentage(s.Progress))
	if s.Paused {
		suffix += " " + localize.Text(language, localize.Paused)
	}
	if line, ok := titleAndSuffix(title, suffix, width); ok {
		return line
	}
	return truncateCells(suffix, width)
}

func cellWidth(s string) int {
	return terminalWidth.StringWidth(strings.ToValidUTF8(s, "�"))
}

func truncateCells(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = strings.ToValidUTF8(s, "�")
	if terminalWidth.StringWidth(s) <= width {
		return s
	}
	return terminalWidth.Truncate(s, width, "…")
}

func padCells(s string, width int) string {
	s = truncateCells(s, width)
	return s + strings.Repeat(" ", max(0, width-cellWidth(s)))
}

func centerCells(s string, width int) string {
	s = truncateCells(s, width)
	padding := max(0, width-cellWidth(s))
	return strings.Repeat(" ", padding/2) + s
}

func sanitizeHuman(s string) string {
	s = strings.ToValidUTF8(s, "�")
	var out strings.Builder
	controlRun := false
	for _, char := range s {
		if unicode.IsControl(char) || isBidiControl(char) {
			if !controlRun {
				out.WriteByte(' ')
				controlRun = true
			}
			continue
		}
		controlRun = false
		out.WriteRune(char)
	}
	return strings.TrimSpace(out.String())
}

func isBidiControl(char rune) bool {
	return char == '\u061c' ||
		char >= '\u200e' && char <= '\u200f' ||
		char >= '\u202a' && char <= '\u202e' ||
		char >= '\u2066' && char <= '\u2069'
}

var glyphs = map[rune][5]string{
	'0': {"###", "# #", "# #", "# #", "###"},
	'1': {"  #", "  #", "  #", "  #", "  #"},
	'2': {"###", "  #", "###", "#  ", "###"},
	'3': {"###", "  #", "###", "  #", "###"},
	'4': {"# #", "# #", "###", "  #", "  #"},
	'5': {"###", "#  ", "###", "  #", "###"},
	'6': {"###", "#  ", "###", "# #", "###"},
	'7': {"###", "  #", "  #", "  #", "  #"},
	'8': {"###", "# #", "###", "# #", "###"},
	'9': {"###", "# #", "###", "  #", "###"},
	':': {" ", "#", " ", "#", " "},
}

func largeDigits(value string) [5]string {
	var lines [5]string
	for _, char := range value {
		glyph := glyphs[char]
		for i := range lines {
			if lines[i] != "" {
				lines[i] += " "
			}
			lines[i] += glyph[i]
		}
	}
	return lines
}
