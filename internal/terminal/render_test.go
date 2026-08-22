package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onlinealarmkur/timer-cli/internal/countdown"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
)

type terminalWriter func([]byte) (int, error)

func (w terminalWriter) Write(data []byte) (int, error) { return w(data) }

func sampleSnapshot() countdown.Snapshot {
	return countdown.Snapshot{
		Initial: time.Minute, Remaining: 30 * time.Second, Elapsed: 30 * time.Second, Progress: .5,
		Target: time.Date(2026, 1, 1, 22, 53, 0, 0, time.Local),
	}
}

func countdownSnapshot(total, remaining time.Duration) countdown.Snapshot {
	progress := float64(0)
	if total > 0 {
		progress = float64(total-remaining) / float64(total)
	}
	return countdown.Snapshot{
		Initial: total, Total: total, Remaining: remaining, Elapsed: total - remaining,
		Progress: progress, Target: time.Date(2026, 1, 1, 22, 53, 0, 0, time.Local),
	}
}

func TestLogicalCompactFrameSpanishExact(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	snapshot.Remaining = 42 * time.Second
	snapshot.Progress = .6
	renderer := New(&bytes.Buffer{}, Options{
		TTY: true, Title: "Té", Language: localize.Spanish,
		Size: func() (int, int) { return 80, 24 },
	})
	frame := renderer.frameFor(snapshot, 80, 24)
	want := "Té · termina a las 22:53 · quedan 00:42\n" +
		"████████████████░░░░░░░░░░░░  60%"
	if frame.body != want || frame.mode != modeCompactTwo || frame.rows != 2 {
		t.Fatalf("frame = %#v, want body %q", frame, want)
	}
}

func TestLogicalCompactFrameExact(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	snapshot.Remaining = 42 * time.Second
	snapshot.Progress = .6
	renderer := New(&bytes.Buffer{}, Options{TTY: true, Title: "Tea", Size: func() (int, int) { return 80, 24 }})
	frame := renderer.frameFor(snapshot, 80, 24)
	want := "Tea · ends 22:53 · 00:42 remaining\n" +
		"████████████████░░░░░░░░░░░░  60%"
	if frame.body != want || frame.mode != modeCompactTwo || frame.rows != 2 {
		t.Fatalf("frame = %#v, want body %q", frame, want)
	}
	if strings.ContainsAny(frame.body, "\r\x1b") {
		t.Fatalf("logical frame contains terminal controls: %q", frame.body)
	}
}

func TestResponsiveFramesAreCellBounded(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	title := "Café 東京 e\u0301 👩‍💻 tea"
	for _, language := range []localize.Language{localize.English, localize.Spanish} {
		renderer := New(&bytes.Buffer{}, Options{TTY: true, Title: title, Language: language})
		for width := 0; width <= 40; width++ {
			for _, height := range []int{1, 2} {
				frame := renderer.frameFor(snapshot, width, height)
				wantRows := 1
				if width >= 32 && height >= 2 {
					wantRows = 2
				}
				if frame.rows != wantRows {
					t.Fatalf("language=%s width=%d height=%d rows=%d, want %d", language, width, height, frame.rows, wantRows)
				}
				for _, line := range strings.Split(frame.body, "\n") {
					if !utf8.ValidString(line) || cellWidth(line) > width {
						t.Fatalf("language=%s width=%d height=%d invalid line %q (cells=%d)", language, width, height, line, cellWidth(line))
					}
				}
			}
		}
	}
}

func TestCellHelpersHandleGraphemeClusters(t *testing.T) {
	t.Parallel()
	samples := []string{"Café", "東京", "e\u0301clair", "👩‍💻 timer", string([]byte{'a', 0xff, 'b'})}
	for _, sample := range samples {
		for width := 0; width <= 40; width++ {
			got := truncateCells(sample, width)
			if !utf8.ValidString(got) || cellWidth(got) > width {
				t.Fatalf("truncateCells(%q, %d) = %q, cells=%d", sample, width, got, cellWidth(got))
			}
			padded := padCells(sample, width)
			if !utf8.ValidString(padded) || cellWidth(padded) != width {
				t.Fatalf("padCells(%q, %d) = %q, cells=%d", sample, width, padded, cellWidth(padded))
			}
			centered := centerCells(sample, width)
			if !utf8.ValidString(centered) || cellWidth(centered) > width {
				t.Fatalf("centerCells(%q, %d) = %q, cells=%d", sample, width, centered, cellWidth(centered))
			}
		}
	}
}

func TestHumanTextSanitization(t *testing.T) {
	t.Parallel()
	got := sanitizeHuman(" Tea\r\n\t\x1b\x7f\u0085B ")
	if got != "Tea B" {
		t.Fatalf("sanitized text = %q", got)
	}

	snapshot := sampleSnapshot()
	renderer := New(&bytes.Buffer{}, Options{TTY: true, Title: "Tea\x1b[2J\nInjected"})
	frame := renderer.frameFor(snapshot, 80, 24)
	if strings.ContainsAny(frame.body, "\r\x1b") || !strings.Contains(frame.body, "Tea [2J Injected") {
		t.Fatalf("sanitized title frame = %q", frame.body)
	}
}

func TestBidiControlPredicateExactSet(t *testing.T) {
	t.Parallel()
	bidiControls := []rune{
		'\u061c',
		'\u200e', '\u200f',
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069',
	}
	for _, char := range bidiControls {
		if !isBidiControl(char) {
			t.Errorf("isBidiControl(%U) = false, want true", char)
		}
		got := sanitizeHuman("A" + string(char) + "B")
		if got != "A B" {
			t.Errorf("sanitizeHuman with %U = %q, want %q", char, got, "A B")
		}
	}

	for _, char := range []rune{'\u061b', '\u061d', '\u200d', '\u2010', '\u2029', '\u202f', '\u2065', '\u206a'} {
		if isBidiControl(char) {
			t.Errorf("isBidiControl(%U) = true, want false", char)
		}
	}
}

func TestHumanTextBidiSanitization(t *testing.T) {
	t.Parallel()
	if got, want := sanitizeHuman("report\u202egpj.exe\u202c"), "report gpj.exe"; got != want {
		t.Fatalf("mixed override attack = %q, want %q", got, want)
	}
	if got, want := sanitizeHuman("A\x00\u202e\u0085\u2066\x1bB"), "A B"; got != want {
		t.Fatalf("mixed adjacent control run = %q, want %q", got, want)
	}

	allBidiControls := "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"
	got := sanitizeHuman("left" + allBidiControls + "right")
	for _, char := range got {
		if isBidiControl(char) {
			t.Fatalf("sanitized human text retained %U: %q", char, got)
		}
	}
}

func TestHumanTextPreservesOrdinaryUnicode(t *testing.T) {
	t.Parallel()
	for _, sample := range []string{
		"العربية",
		"עברית",
		"Español",
		"東京",
		"e\u0301",
		"👩‍💻",
	} {
		input := string([]byte{0xff}) + sample + string([]byte{0xfe})
		want := strings.ToValidUTF8(input, "�")
		if got := sanitizeHuman(input); !bytes.Equal([]byte(got), []byte(want)) {
			t.Errorf("sanitizeHuman(%q) = %q, want byte-preserved %q", input, got, want)
		}
	}
}

func TestBidiControlsAreSanitizedAtHumanOutputBoundaries(t *testing.T) {
	t.Parallel()
	renderer := New(&bytes.Buffer{}, Options{TTY: true, Title: "Tea\u202eevil\u202c"})
	frame := renderer.frameFor(sampleSnapshot(), 80, 24)
	if !strings.Contains(frame.body, "Tea evil") {
		t.Fatalf("sanitized title frame = %q", frame.body)
	}
	for _, char := range frame.body {
		if isBidiControl(char) {
			t.Fatalf("title frame retained %U: %q", char, frame.body)
		}
	}

	var output bytes.Buffer
	if err := New(&output, Options{FinalOnly: true}).Finish(
		sampleSnapshot(), "completed", "Done\u2066\x1b\u2069now", time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Done now\n"; got != want {
		t.Fatalf("sanitized completion = %q, want %q", got, want)
	}
}

func TestTTYRenderAndCleanup(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{TTY: true, Title: "Tea", ASCII: true, Size: func() (int, int) { return 80, 24 }})
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, hideCursor) || !strings.Contains(got, showCursor) || !strings.Contains(got, "Tea") {
		t.Fatalf("TTY output did not manage cursor/title: %q", got)
	}
	if strings.Contains(got, "\x1b[0m") {
		t.Fatalf("cleanup emitted an unnecessary SGR reset: %q", got)
	}
	if strings.Count(got, showCursor) != 1 {
		t.Fatalf("cursor was restored %d times, want once: %q", strings.Count(got, showCursor), got)
	}
	if strings.Count(got, clearOwnedRows(2)) != 1 {
		t.Fatalf("compact frame was cleared %d times, want once: %q", strings.Count(got, clearOwnedRows(2)), got)
	}
	if renderer.hasFrame || renderer.cursorHidden {
		t.Fatalf("renderer retained cleanup state: hasFrame=%v cursorHidden=%v", renderer.hasFrame, renderer.cursorHidden)
	}
}

func TestFullscreenCloseClearsScreen(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{
		TTY: true, Fullscreen: true, Size: func() (int, int) { return 80, 24 },
	})
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	beforeClose := output.Len()
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String()[beforeClose:], clearScreen+showCursor; got != want {
		t.Fatalf("fullscreen cleanup = %q, want %q", got, want)
	}
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	if output.Len() != beforeClose+len(clearScreen)+len(showCursor) {
		t.Fatalf("repeated close emitted output: %q", output.String())
	}
}

func TestFullscreenLoopCarriesCompletionNoticeIntoRestartedFrame(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{
		TTY: true, Fullscreen: true, Title: "Tea",
		Size: func() (int, int) { return 80, 24 },
	})
	finished := sampleSnapshot()
	finished.Remaining = 0
	finished.Progress = 1
	finished.Finished = true
	if err := renderer.Render(finished); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Finish(finished, "completed", "Tea is ready", finished.ObservedAt); err != nil {
		t.Fatal(err)
	}

	restarted := sampleSnapshot()
	restarted.Remaining = time.Minute
	restarted.Elapsed = 0
	restarted.Progress = 0
	restarted.Target = restarted.ObservedAt.Add(time.Minute)
	if err := renderer.Render(restarted); err != nil {
		t.Fatal(err)
	}
	screen := output.String()[strings.LastIndex(output.String(), clearScreen):]
	for _, want := range []string{"Tea is ready", "Tea", "01:00", "0%"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("restarted fullscreen screen omits %q: %q", want, screen)
		}
	}

	restarted.ObservedAt = restarted.ObservedAt.Add(cycleNoticeDuration)
	restarted.Remaining -= cycleNoticeDuration
	restarted.Elapsed += cycleNoticeDuration
	restarted.Progress = float64(restarted.Elapsed) / float64(restarted.Total)
	if err := renderer.Render(restarted); err != nil {
		t.Fatal(err)
	}
	screen = output.String()[strings.LastIndex(output.String(), clearScreen):]
	if strings.Contains(screen, "Tea is ready") {
		t.Fatalf("expired completion notice remained visible: %q", screen)
	}
}

func TestFinishOutputModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    Options
		status  string
		message string
		want    string
	}{
		{name: "quiet completion", opts: Options{Quiet: true}, status: "completed", message: "Done"},
		{
			name: "ordinary cancellation", status: "canceled",
			want: "remaining=00:30 progress=50% state=running\n",
		},
		{name: "final-only cancellation", opts: Options{FinalOnly: true}, status: "canceled", want: "Timer canceled.\n"},
		{name: "final-only completion", opts: Options{FinalOnly: true}, status: "completed", message: "Done", want: "Done\n"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			renderer := New(&output, test.opts)
			if err := renderer.Render(sampleSnapshot()); err != nil {
				t.Fatal(err)
			}
			if err := renderer.Finish(sampleSnapshot(), test.status, test.message, time.Now()); err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("output = %q, want %q", output.String(), test.want)
			}
		})
	}
}

func TestLineEndingsFollowRawTerminalCapability(t *testing.T) {
	t.Parallel()
	snapshot := countdownSnapshot(time.Minute, 30*time.Second)
	tests := []struct {
		name string
		opts Options
		run  func(*Renderer) error
		want string
	}{
		{
			name: "redirected LF", run: func(renderer *Renderer) error { return renderer.Render(snapshot) },
			want: "remaining=00:30 progress=50% state=running\n",
		},
		{
			name: "raw terminal record CRLF", opts: Options{RawTerminal: true},
			run:  func(renderer *Renderer) error { return renderer.Render(snapshot) },
			want: "remaining=00:30 progress=50% state=running\r\n",
		},
		{
			name: "redirected completion LF", opts: Options{FinalOnly: true},
			run: func(renderer *Renderer) error {
				return renderer.Finish(snapshot, "completed", "Done", time.Now())
			},
			want: "Done\n",
		},
		{
			name: "raw terminal completion CRLF", opts: Options{RawTerminal: true, FinalOnly: true},
			run: func(renderer *Renderer) error {
				return renderer.Finish(snapshot, "completed", "Done", time.Now())
			},
			want: "Done\r\n",
		},
		{
			name: "raw terminal cancellation CRLF", opts: Options{RawTerminal: true, FinalOnly: true},
			run: func(renderer *Renderer) error {
				return renderer.Finish(snapshot, "canceled", "", time.Now())
			},
			want: "Timer canceled.\r\n",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			if err := test.run(New(&output, test.opts)); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != test.want {
				t.Fatalf("output = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRawTerminalJSONChangesOnlyLineEnding(t *testing.T) {
	t.Parallel()
	snapshot := countdownSnapshot(90*time.Second, time.Minute)
	finishedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	encode := func(rawTerminal bool) []byte {
		var output bytes.Buffer
		renderer := New(&output, Options{JSON: true, RawTerminal: rawTerminal, Title: "<title>&"})
		if err := renderer.Finish(snapshot, "completed", string([]byte{'D', 0xff}), finishedAt); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}

	redirected := encode(false)
	rawTerminal := encode(true)
	if !bytes.HasSuffix(redirected, []byte("}\n")) {
		t.Fatalf("redirected JSON ending = %q, want LF", redirected)
	}
	if !bytes.HasSuffix(rawTerminal, []byte("}\r\n")) {
		t.Fatalf("raw-terminal JSON ending = %q, want CRLF", rawTerminal)
	}
	redirectedPayload := bytes.TrimSuffix(redirected, []byte("\n"))
	rawPayload := bytes.TrimSuffix(rawTerminal, []byte("\r\n"))
	if !bytes.Equal(rawPayload, redirectedPayload) {
		t.Fatalf("JSON payload changed with line-ending capability: redirected=%q raw=%q", redirectedPayload, rawPayload)
	}
	if !json.Valid(redirected) || !json.Valid(rawTerminal) {
		t.Fatalf("invalid JSON: redirected=%q raw=%q", redirected, rawTerminal)
	}
	if !bytes.Contains(redirectedPayload, []byte(`"title":"\u003ctitle\u003e\u0026"`)) {
		t.Fatalf("JSON default HTML escaping changed: %q", redirectedPayload)
	}
	var decoded struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(redirectedPayload, &decoded); err != nil {
		t.Fatalf("decode JSON result: %v", err)
	}
	if got, want := decoded.Message, "D\uFFFD"; got != want {
		t.Fatalf("JSON invalid UTF-8 replacement = %q, want %q", got, want)
	}
}

func TestSpanishHumanOutputKeepsRedirectedRecordsStable(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	snapshot.Paused = true

	var redirected bytes.Buffer
	if err := New(&redirected, Options{Language: localize.Spanish}).Render(snapshot); err != nil {
		t.Fatal(err)
	}
	if got, want := redirected.String(), "remaining=00:30 progress=50% state=paused\n"; got != want {
		t.Fatalf("redirected output = %q, want %q", got, want)
	}

	var canceled bytes.Buffer
	if err := New(&canceled, Options{FinalOnly: true, Language: localize.Spanish}).Finish(snapshot, "canceled", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, want := canceled.String(), "Temporizador cancelado.\n"; got != want {
		t.Fatalf("cancellation = %q, want %q", got, want)
	}

	renderer := New(&bytes.Buffer{}, Options{TTY: true, Fullscreen: true, Controls: true, Language: localize.Spanish})
	frame := renderer.frameFor(snapshot, 100, 24)
	for _, want := range []string{"quedan 00:30", "EN PAUSA", "Espacio pausar/reanudar"} {
		if !strings.Contains(frame.body, want) {
			t.Errorf("Spanish fullscreen frame omits %q: %q", want, frame.body)
		}
	}
}

func TestPausedRedirectedOutput(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	snapshot := sampleSnapshot()
	snapshot.Paused = true
	if err := New(&output, Options{}).Render(snapshot); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "remaining=00:30 progress=50% state=paused\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRedirectedAdaptiveCadenceExactBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		total      time.Duration
		cadence    time.Duration
		maxRecords int
	}{
		{name: "30 minutes", total: 30 * time.Minute, cadence: time.Second},
		{name: "2 hours", total: 2 * time.Hour, cadence: time.Minute},
		{name: "30 days", total: 30 * 24 * time.Hour, cadence: 10 * time.Minute, maxRecords: 4322},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			renderer := New(&output, Options{})
			buckets := int(test.total / test.cadence)
			for bucket := buckets; bucket >= 1; bucket-- {
				boundary := time.Duration(bucket) * test.cadence
				if err := renderer.Render(countdownSnapshot(test.total, boundary)); err != nil {
					t.Fatal(err)
				}
				withinBucket := time.Duration(bucket-1)*test.cadence + time.Nanosecond
				if err := renderer.Render(countdownSnapshot(test.total, withinBucket)); err != nil {
					t.Fatal(err)
				}
			}
			if err := renderer.Render(countdownSnapshot(test.total, 0)); err != nil {
				t.Fatal(err)
			}

			records := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
			wantRecords := buckets + 1
			if len(records) != wantRecords {
				t.Fatalf("records = %d, want %d", len(records), wantRecords)
			}
			if test.maxRecords > 0 && len(records) > test.maxRecords {
				t.Fatalf("records = %d, maximum %d", len(records), test.maxRecords)
			}
			for index, record := range records {
				remaining := test.total - time.Duration(index)*test.cadence
				if index == len(records)-1 {
					remaining = 0
				}
				wantPrefix := "remaining=" + FormatDuration(remaining) + " "
				if !strings.HasPrefix(record, wantPrefix) {
					t.Fatalf("record %d = %q, want prefix %q", index, record, wantPrefix)
				}
				if index < len(records)-1 && strings.HasPrefix(record, "remaining=00:00 ") {
					t.Fatalf("record %d showed zero before completion: %q", index, record)
				}
			}
		})
	}
}

func TestRedirectedAdaptiveCadenceDeduplicatesSnapshots(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{})
	snapshot := countdownSnapshot(2*time.Hour, 90*time.Minute)
	for range 2 {
		if err := renderer.Render(snapshot); err != nil {
			t.Fatal(err)
		}
	}
	snapshot = countdownSnapshot(2*time.Hour, 89*time.Minute+time.Nanosecond)
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	if records := strings.Count(output.String(), "\n"); records != 1 {
		t.Fatalf("records = %d, want 1; output=%q", records, output.String())
	}
}

func TestRedirectedAdaptiveCadenceEmitsPauseAndResume(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{})
	snapshot := countdownSnapshot(2*time.Hour, 90*time.Minute)
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Paused = true
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Paused = false
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	want := "remaining=01:30:00 progress=25% state=running\n" +
		"remaining=01:30:00 progress=25% state=paused\n" +
		"remaining=01:30:00 progress=25% state=running\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestRedirectedAdaptiveCadenceEmitsInitialAndSingleZero(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{})
	initial := countdownSnapshot(30*24*time.Hour, 29*24*time.Hour+59*time.Minute)
	if err := renderer.Render(initial); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Render(countdownSnapshot(30*24*time.Hour, initial.Remaining-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := renderer.Render(countdownSnapshot(30*24*time.Hour, 0)); err != nil {
			t.Fatal(err)
		}
	}
	records := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(records) != 2 || !strings.HasPrefix(records[0], "remaining=696:59:00 ") ||
		!strings.HasPrefix(records[1], "remaining=00:00 ") {
		t.Fatalf("records = %#v", records)
	}
}

func TestRedirectedAdaptiveCadenceNeverShowsEarlyZero(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{})
	if err := renderer.Render(countdownSnapshot(30*time.Minute, 900*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Render(countdownSnapshot(30*time.Minute, time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Render(countdownSnapshot(30*time.Minute, 0)); err != nil {
		t.Fatal(err)
	}
	want := "remaining=00:01 progress=99% state=running\n" +
		"remaining=00:00 progress=100% state=running\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestAdaptiveCadenceDoesNotChangeSilentOrTTYRender(t *testing.T) {
	t.Parallel()
	for _, opts := range []Options{{Quiet: true}, {FinalOnly: true}, {JSON: true}} {
		var output bytes.Buffer
		renderer := New(&output, opts)
		if err := renderer.Render(countdownSnapshot(2*time.Hour, 2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := renderer.Render(countdownSnapshot(2*time.Hour, 2*time.Hour-time.Second)); err != nil {
			t.Fatal(err)
		}
		if output.Len() != 0 {
			t.Fatalf("options=%+v output=%q, want silent render", opts, output.String())
		}
	}

	var output bytes.Buffer
	renderer := New(&output, Options{TTY: true, Size: func() (int, int) { return 80, 24 }})
	if err := renderer.Render(countdownSnapshot(2*time.Hour, 2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Render(countdownSnapshot(2*time.Hour, 2*time.Hour-time.Second)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "02:00:00 remaining") || !strings.Contains(output.String(), "01:59:59 remaining") {
		t.Fatalf("TTY output did not retain per-frame rendering: %q", output.String())
	}
}

func TestRendererPropagatesWriterFailures(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("output unavailable")
	failing := terminalWriter(func([]byte) (int, error) { return 0, writeErr })
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "plain render",
			run:  func() error { return New(failing, Options{}).Render(sampleSnapshot()) },
		},
		{
			name: "JSON finish",
			run: func() error {
				return New(failing, Options{JSON: true}).Finish(sampleSnapshot(), "completed", "Done", time.Now())
			},
		},
		{
			name: "TTY cursor hide",
			run:  func() error { return New(failing, Options{TTY: true}).Render(sampleSnapshot()) },
		},
		{
			name: "TTY finish clear",
			run: func() error {
				return New(failing, Options{TTY: true}).Finish(sampleSnapshot(), "completed", "Done", time.Now())
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.run(); !errors.Is(err, writeErr) {
				t.Fatalf("error = %v, want %v", err, writeErr)
			}
		})
	}

	call := 0
	failBody := terminalWriter(func(data []byte) (int, error) {
		call++
		if call == 2 {
			return 0, writeErr
		}
		return len(data), nil
	})
	if err := New(failBody, Options{TTY: true}).Render(sampleSnapshot()); !errors.Is(err, writeErr) {
		t.Fatalf("TTY frame write error = %v, want %v", err, writeErr)
	}

	var output bytes.Buffer
	failClose := false
	closeWriter := terminalWriter(func(data []byte) (int, error) {
		if failClose {
			return 0, writeErr
		}
		return output.Write(data)
	})
	renderer := New(closeWriter, Options{TTY: true})
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	failClose = true
	if err := renderer.Close(); !errors.Is(err, writeErr) {
		t.Fatalf("Close() error = %v, want %v", err, writeErr)
	}
}

func TestCloseAttemptsCursorRestoreAfterClearFailure(t *testing.T) {
	t.Parallel()
	clearErr := errors.New("clear failed")
	var output bytes.Buffer
	call := 0
	writer := terminalWriter(func(data []byte) (int, error) {
		call++
		if call == 3 {
			return 0, clearErr
		}
		return output.Write(data)
	})
	renderer := New(writer, Options{TTY: true, Size: func() (int, int) { return 80, 24 }})
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Close(); !errors.Is(err, clearErr) {
		t.Fatalf("Close() error = %v, want %v", err, clearErr)
	}
	if !strings.HasSuffix(output.String(), showCursor) {
		t.Fatalf("cursor was not restored after clear failure: %q", output.String())
	}
	if !renderer.hasFrame || renderer.cursorHidden {
		t.Fatalf("cleanup state = hasFrame=%v cursorHidden=%v, want true/false", renderer.hasFrame, renderer.cursorHidden)
	}
}

func TestCloseJoinsClearAndCursorErrors(t *testing.T) {
	t.Parallel()
	clearErr := errors.New("clear failed")
	cursorErr := errors.New("cursor restore failed")
	call := 0
	writer := terminalWriter(func(data []byte) (int, error) {
		call++
		switch call {
		case 3:
			return 0, clearErr
		case 4:
			return 0, cursorErr
		default:
			return len(data), nil
		}
	})
	renderer := New(writer, Options{TTY: true, Size: func() (int, int) { return 80, 24 }})
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	err := renderer.Close()
	if !errors.Is(err, clearErr) || !errors.Is(err, cursorErr) {
		t.Fatalf("Close() error = %v, want joined %v and %v", err, clearErr, cursorErr)
	}
	if call != 4 {
		t.Fatalf("writer calls = %d, want 4", call)
	}
}

func TestRendererDetectsShortWrites(t *testing.T) {
	t.Parallel()
	short := terminalWriter(func(data []byte) (int, error) {
		return max(0, len(data)-1), nil
	})
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "plain render", run: func() error { return New(short, Options{}).Render(sampleSnapshot()) }},
		{
			name: "JSON finish",
			run: func() error {
				return New(short, Options{JSON: true}).Finish(sampleSnapshot(), "completed", "Done", time.Now())
			},
		},
		{name: "TTY render", run: func() error { return New(short, Options{TTY: true}).Render(sampleSnapshot()) }},
		{
			name: "TTY finish",
			run: func() error {
				return New(short, Options{TTY: true}).Finish(sampleSnapshot(), "completed", "Done", time.Now())
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.run(); !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("error = %v, want %v", err, io.ErrShortWrite)
			}
		})
	}
}

func TestNonTTYHasNoANSI(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{})
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	want := "remaining=00:30 progress=50% state=running\n"
	if output.String() != want || strings.ContainsAny(output.String(), "\x1b\a") {
		t.Fatalf("non-TTY output = %q, want %q", output.String(), want)
	}
}

func TestNarrowAndPausedFallbacks(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	renderer := New(&bytes.Buffer{}, Options{TTY: true, Title: "Tea"})
	if got := renderer.frameFor(snapshot, 19, 2).body; got != "Tea · 00:30 50%" {
		t.Fatalf("narrow frame = %q", got)
	}
	snapshot.Paused = true
	got := renderer.frameFor(snapshot, 32, 2).body
	if strings.Contains(got, "ends") || !strings.Contains(got, "Tea · 00:30 remaining · PAUSED") {
		t.Fatalf("paused frame = %q", got)
	}
	got = renderer.frameFor(snapshot, 80, 24).body
	if strings.Contains(got, "ends") || !strings.Contains(got, "Tea · 00:30 remaining · PAUSED") {
		t.Fatalf("wide paused frame = %q", got)
	}
}

func TestTTYRenderCachesLogicalFrame(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	width := 80
	renderer := New(&output, Options{TTY: true, Title: "Tea", Size: func() (int, int) { return width, 24 }})
	snapshot := sampleSnapshot()
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	firstLen := output.Len()
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	if output.Len() != firstLen {
		t.Fatalf("identical frame wrote %d additional bytes", output.Len()-firstLen)
	}
	width = 79
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	resizeLen := output.Len()
	if resizeLen == firstLen {
		t.Fatal("resize did not redraw")
	}
	snapshot.Paused = true
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	if output.Len() == resizeLen {
		t.Fatal("state change did not redraw")
	}
}

func TestLoopCompletionClearsOwnedRows(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{TTY: true, Loop: true, Title: "Tea", Size: func() (int, int) { return 80, 24 }})
	snapshot := sampleSnapshot()
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Remaining -= time.Second
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Finish(snapshot, "completed", "Done\r\n\x1b[31m\tnow", time.Now()); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Count(got, "\x1b[1A") != 2 {
		t.Fatalf("two-row updates did not clear owned rows: %q", got)
	}
	if !strings.HasSuffix(got, "Done [31m now\r\n") {
		t.Fatalf("sanitized completion = %q", got)
	}
}

func TestCompactCompletionKeepsFinalFrameAndMessage(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{TTY: true, Title: "Focus Session", Size: func() (int, int) { return 80, 24 }})
	finished := countdownSnapshot(5*time.Second, 0)
	finished.Finished = true
	if err := renderer.Render(finished); err != nil {
		t.Fatal(err)
	}
	beforeFinish := output.Len()
	if err := renderer.Finish(finished, "completed", "Done\r\n\x1b[31m\tnow", finished.ObservedAt); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String()[beforeFinish:], "\r\nDone [31m now\r\n"; got != want {
		t.Fatalf("completion suffix = %q, want %q", got, want)
	}
	if !strings.Contains(output.String(), "Focus Session") || !strings.Contains(output.String(), "00:00 remaining") ||
		!strings.Contains(output.String(), "100%") {
		t.Fatalf("final frame was not retained: %q", output.String())
	}
	beforeClose := output.Len()
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String()[beforeClose:], showCursor; got != want {
		t.Fatalf("completion cleanup = %q, want only cursor restoration %q", got, want)
	}
}

func TestNarrowFullscreenFallsBackToCompact(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{TTY: true, Fullscreen: true, ASCII: true, Size: func() (int, int) { return 20, 8 }})
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), clearScreen) || !strings.Contains(output.String(), "00:30") {
		t.Fatalf("narrow output = %q", output.String())
	}
}

func TestFullscreenFallsBackWhenContentExceedsHeight(t *testing.T) {
	t.Parallel()
	snapshot := sampleSnapshot()
	snapshot.Paused = true
	renderer := New(&bytes.Buffer{}, Options{TTY: true, Fullscreen: true, Controls: true})
	frame := renderer.frameFor(snapshot, 80, 10)
	if frame.mode == modeFullscreen || !strings.Contains(frame.body, "PAUSED") {
		t.Fatalf("frame = %#v", frame)
	}
}

func TestFreshRendererCloseAndEmptyOwnedRowsAreNoOps(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{TTY: true})
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 || clearOwnedRows(0) != "" || clearOwnedRows(-1) != "" {
		t.Fatalf("output=%q zero=%q negative=%q", output.String(), clearOwnedRows(0), clearOwnedRows(-1))
	}
}

func TestFullscreenAndResize(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	width := 80
	renderer := New(&output, Options{TTY: true, Fullscreen: true, Controls: true, Size: func() (int, int) { return width, 24 }})
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	width = 40
	if err := renderer.Render(sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), clearScreen) || !strings.Contains(output.String(), "Space pause") ||
		!strings.Contains(output.String(), "ends 22:53") || !strings.Contains(output.String(), "50%") {
		t.Fatalf("fullscreen output = %q", output.String())
	}
}

func TestJSONResult(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	title := "Tea\x1b[2J\u202e"
	message := "Done\r\nnow\u2066"
	snapshot := sampleSnapshot()
	snapshot.Initial = time.Minute
	snapshot.Total = 90 * time.Second
	snapshot.Elapsed = 30 * time.Second
	renderer := New(&output, Options{JSON: true, Title: title})
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	finishedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := renderer.Finish(snapshot, "completed", message, finishedAt); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(output.String(), "\n") || strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("JSON output is not exactly one newline-terminated object: %q", output.String())
	}
	if strings.ContainsAny(output.String(), "\x1b\a") {
		t.Fatalf("JSON output contains raw terminal control bytes: %q", output.String())
	}
	var result struct {
		Status                 string  `json:"status"`
		DurationSeconds        float64 `json:"duration_seconds"`
		InitialDurationSeconds float64 `json:"initial_duration_seconds"`
		ElapsedSeconds         float64 `json:"elapsed_seconds"`
		Title                  string  `json:"title"`
		Message                string  `json:"message"`
		FinishedAt             string  `json:"finished_at"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", output.String(), err)
	}
	if result.Status != "completed" || result.DurationSeconds != 90 || result.InitialDurationSeconds != 60 ||
		result.ElapsedSeconds != 30 || result.Title != title || result.Message != message ||
		result.FinishedAt != finishedAt.Format(time.RFC3339) {
		t.Fatalf("result = %#v", result)
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	if got := FormatDuration(-time.Second); got != "00:00" {
		t.Fatalf("negative display = %s", got)
	}
	if got := FormatDuration(1500 * time.Millisecond); got != "00:02" {
		t.Fatalf("rounded display = %s", got)
	}
	if got := FormatDuration(90 * time.Minute); got != "01:30:00" {
		t.Fatalf("hour display = %s", got)
	}
}

func TestPercentageReservesOneHundredForCompletion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		progress float64
		want     int
	}{
		{progress: -0.1, want: 0},
		{progress: 0.005, want: 0},
		{progress: 0.599, want: 59},
		{progress: 0.999999, want: 99},
		{progress: 1, want: 100},
		{progress: 1.1, want: 100},
	}
	for _, test := range tests {
		if got := percentage(test.progress); got != test.want {
			t.Errorf("percentage(%v) = %d, want %d", test.progress, got, test.want)
		}
	}
}

func TestPhysicalRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		body  string
		width int
		want  int
	}{
		{name: "single short line fits in one row", body: "hi", width: 40, want: 1},
		{name: "a 60-cell line wraps into 2 rows at width 40", body: strings.Repeat("x", 60), width: 40, want: 2},
		{name: "two 45-cell lines wrap into 4 rows total at width 40", body: strings.Repeat("x", 45) + "\n" + strings.Repeat("x", 45), width: 40, want: 4},
		{name: "non-positive width counts each line once", body: "a\nb\nc", width: 0, want: 3},
		// A written frame occupies at least the cursor row, matching
		// clearOwnedRows's rows<=0 guard (0 or negative rows clear nothing,
		// so a frame that was actually written must count at least 1).
		{name: "empty body still occupies the cursor row", body: "", width: 40, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := physicalRows(tc.body, tc.width); got != tc.want {
				t.Fatalf("physicalRows(%q, %d) = %d, want %d", tc.body, tc.width, got, tc.want)
			}
		})
	}
}

func TestNoShrinkRedrawClearMatchesLogicalRows(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	renderer := New(&output, Options{TTY: true, Title: "Tea", Size: func() (int, int) { return 80, 24 }})
	snapshot := sampleSnapshot()
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	oldFrame := renderer.lastFrame
	before := output.Len()
	snapshot.Remaining -= time.Second
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	got := output.String()[before:]
	want := clearOwnedRows(oldFrame.rows) + terminalText(renderer.lastFrame.body)
	if got != want {
		t.Fatalf("no-shrink redraw clear changed: got %q, want %q", got, want)
	}
}

func TestNarrowingEscalatesRedrawClear(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	width := 80
	title := strings.Repeat("A", 60)
	renderer := New(&output, Options{TTY: true, Title: title, Size: func() (int, int) { return width, 24 }})
	snapshot := sampleSnapshot()
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	oldFrame := renderer.lastFrame
	if oldFrame.mode != modeCompactTwo {
		t.Fatalf("setup: want a two-line compact frame, got %#v", oldFrame)
	}
	wantPhysical := physicalRows(oldFrame.body, 40)
	if wantPhysical <= oldFrame.rows {
		t.Fatalf("setup: narrowing to 40 must escalate rows past the logical count, got physical=%d logical=%d body=%q", wantPhysical, oldFrame.rows, oldFrame.body)
	}

	before := output.Len()
	width = 40
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	got := output.String()[before:]
	wantClear := clearOwnedRows(wantPhysical)
	if !strings.HasPrefix(got, wantClear) {
		t.Fatalf("narrowed redraw clear = %q, want prefix %q", got, wantClear)
	}
}

func TestNarrowingEscalatesCloseClear(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	width := 80
	title := strings.Repeat("A", 60)
	renderer := New(&output, Options{TTY: true, Title: title, Size: func() (int, int) { return width, 24 }})
	snapshot := sampleSnapshot()
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	oldFrame := renderer.lastFrame
	wantPhysical := physicalRows(oldFrame.body, 40)
	if wantPhysical <= oldFrame.rows {
		t.Fatalf("setup: narrowing to 40 must escalate rows past the logical count, got physical=%d logical=%d body=%q", wantPhysical, oldFrame.rows, oldFrame.body)
	}

	before := output.Len()
	width = 40
	if err := renderer.Close(); err != nil {
		t.Fatal(err)
	}
	got := output.String()[before:]
	wantClear := clearOwnedRows(wantPhysical)
	if !strings.HasPrefix(got, wantClear) {
		t.Fatalf("narrowed close clear = %q, want prefix %q", got, wantClear)
	}
}

func TestNarrowingEscalatesFinishClear(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	width := 80
	title := strings.Repeat("A", 60)
	renderer := New(&output, Options{TTY: true, Title: title, Size: func() (int, int) { return width, 24 }})
	snapshot := sampleSnapshot()
	if err := renderer.Render(snapshot); err != nil {
		t.Fatal(err)
	}
	oldFrame := renderer.lastFrame
	wantPhysical := physicalRows(oldFrame.body, 40)
	if wantPhysical <= oldFrame.rows {
		t.Fatalf("setup: narrowing to 40 must escalate rows past the logical count, got physical=%d logical=%d body=%q", wantPhysical, oldFrame.rows, oldFrame.body)
	}

	before := output.Len()
	width = 40
	if err := renderer.Finish(snapshot, "canceled", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	got := output.String()[before:]
	wantClear := clearOwnedRows(wantPhysical)
	if !strings.HasPrefix(got, wantClear) {
		t.Fatalf("narrowed finish clear = %q, want prefix %q", got, wantClear)
	}
}
