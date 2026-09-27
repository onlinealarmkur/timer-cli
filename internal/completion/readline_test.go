//go:build darwin || linux

package completion

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
)

func TestBashReadlinePreservesCompletedArguments(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	tests := []struct {
		name       string
		input      string
		wordBreaks string
		want       string
	}{
		{name: "colon duration", input: "timer-cli 01:\t", want: "<01:30>"},
		{name: "partial colon duration", input: "timer-cli 01:3\t", want: "<01:30>"},
		{name: "equals language", input: "timer-cli --lang=es\t", want: "<--lang=es>"},
		{name: "partial equals language", input: "timer-cli --lang=a\t", want: "<--lang=auto>"},
		{name: "inside language token", input: "timer-cli --lang=auto" + strings.Repeat("\x1b[D", 3) + "\t\x05", want: "<--lang=auto>"},
		{name: "inside duration token", input: "timer-cli 01:30\x1b[D\t\x05", want: "<01:30>"},
		{name: "inside language before more arguments", input: "timer-cli --lang=auto --quiet 1s" + strings.Repeat("\x1b[D", len("uto --quiet 1s")) + "\t\x05", want: "<--lang=auto><--quiet><1s>"},
		{name: "inside token after unicode title", input: "timer-cli --title 'Estudiar 日本語' --lang=auto" + strings.Repeat("\x1b[D", 3) + "\t\x05", want: "<--title><Estudiar 日本語><--lang=auto>"},
		{name: "token boundary after unicode title", input: "timer-cli --title 'Estudiar 日本語' --lang=a\t", want: "<--title><Estudiar 日本語><--lang=auto>"},
		{name: "separate language", input: "timer-cli --lang a\t", want: "<--lang><auto>"},
		{name: "after equals title", input: "timer-cli --title=Tea 01:\t", want: "<--title=Tea><01:30>"},
		{name: "quoted title", input: "timer-cli --title 'a:b = c' 01:\t", want: "<--title><a:b = c><01:30>"},
		{name: "empty equals title", input: "timer-cli --title=\"\" 01:\t", want: "<--title=><01:30>"},
		{name: "quoted equals title", input: "timer-cli --title=\"a:b = c\" 01:\t", want: "<--title=a:b = c><01:30>"},
		{name: "escaped title", input: "timer-cli --title=a\\ b:c 01:\t", want: "<--title=a b:c><01:30>"},
		{name: "spaced equals title", input: "timer-cli --title = 01:\t", want: "<--title><=><01:30>"},
		{name: "language-looking title", input: "timer-cli --title --lang=a\t", want: "<--title><--lang=a>"},
		{name: "language-looking message", input: "timer-cli --message --lang=a\t", want: "<--message><--lang=a>"},
		{name: "title consumes language flag", input: "timer-cli --title --lang 01:\t", want: "<--title><--lang><01:30>"},
		{name: "real language after literal title", input: "timer-cli --title --lang --lang a\t", want: "<--title><--lang><--lang><auto>"},
		{name: "middle of command duration", input: "timer-cli 01: --no-bell" + strings.Repeat("\x1b[D", len(" --no-bell")) + "\t\x05", want: "<01:30><--no-bell>"},
		{name: "middle of command language", input: "timer-cli --lang=a --quiet 1s" + strings.Repeat("\x1b[D", len(" --quiet 1s")) + "\t\x05", want: "<--lang=auto><--quiet><1s>"},
		{name: "language before command", input: "timer-cli --lang=es completion b\t", want: "<--lang=es><completion><bash>"},
		{name: "language after command", input: "timer-cli completion --lang=es b\t", want: "<completion><--lang=es><bash>"},
		{name: "quoted language before command", input: "timer-cli --lang=\"es\" completion b\t", want: "<--lang=es><completion><bash>"},
		{name: "preceding command", input: "true; timer-cli --lang=es completion b\t", want: "<--lang=es><completion><bash>"},
		{name: "custom word breaks colon", input: "timer-cli 01:\t", wordBreaks: "COMP_WORDBREAKS=${COMP_WORDBREAKS//[:=]/}", want: "<01:30>"},
		{name: "custom word breaks equals", input: "timer-cli --lang=a\t", wordBreaks: "COMP_WORDBREAKS=${COMP_WORDBREAKS//[:=]/}", want: "<--lang=auto>"},
		{name: "inside token with custom word breaks", input: "timer-cli --lang=auto" + strings.Repeat("\x1b[D", 3) + "\t\x05", wordBreaks: "COMP_WORDBREAKS=${COMP_WORDBREAKS//[:=]/}", want: "<--lang=auto>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := runBashReadline(t, bash, test.input, test.wordBreaks)
			want := "__TIMER_CLI_ARGS__" + test.want + "__TIMER_CLI_END__"
			if !strings.Contains(output, want) {
				t.Fatalf("Readline changed submitted arguments; want %q in %q", want, output)
			}
		})
	}
}

func runBashReadline(t *testing.T, bash, input, wordBreaks string) string {
	t.Helper()
	script, err := ScriptFor("bash", localize.English)
	if err != nil {
		t.Fatal(err)
	}
	// The capture command observes the arguments after actual Readline insertion
	// and shell parsing. No countdown is run and no user startup files are read.
	script += `
unset HISTFILE
PS1='__TIMER_CLI_READY__ '
bind 'set editing-mode emacs'
bind 'set completion-ignore-case off'
bind 'set show-all-if-ambiguous off'
` + wordBreaks + `
timer_cli_original_wordbreaks=$COMP_WORDBREAKS
timer-cli() {
  [[ "$COMP_WORDBREAKS" == "$timer_cli_original_wordbreaks" ]] || exit 9
  printf '\n__TIMER_CLI_ARGS__'
  printf '<%s>' "$@"
  printf '__TIMER_CLI_END__\n'
  exit
}
`
	rcfile := filepath.Join(t.TempDir(), "bashrc")
	if err := os.WriteFile(rcfile, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	command := exec.CommandContext(ctx, bash, "--noprofile", "--rcfile", rcfile, "-i")
	command.Env = append(os.Environ(), "INPUTRC=/dev/null", "TERM=xterm-256color", "BASH_SILENCE_DEPRECATION_WARNING=1")
	master, err := pty.StartWithSize(command, &pty.Winsize{Rows: 24, Cols: 160})
	if err != nil {
		cancel()
		t.Fatalf("start interactive Bash: %v", err)
	}
	var output bytes.Buffer
	ready := make(chan struct{})
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		notified := false
		buffer := make([]byte, 4096)
		for {
			n, readErr := master.Read(buffer)
			output.Write(buffer[:n])
			if !notified && bytes.Contains(output.Bytes(), []byte("__TIMER_CLI_READY__")) {
				notified = true
				close(ready)
			}
			if readErr != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		_ = command.Wait()
		_ = master.Close()
		<-readDone
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("interactive Bash did not become ready")
	}
	if _, err := io.WriteString(master, input+"\n"); err != nil {
		t.Fatalf("type completion command: %v", err)
	}
	waitErr := command.Wait()
	// Drain the PTY before closing it so the final captured arguments are not
	// lost when the shell exits before the reader gets scheduled.
	select {
	case <-readDone:
	case <-ctx.Done():
		_ = master.Close()
		<-readDone
		t.Fatalf("interactive Bash output did not close: %q", output.String())
	}
	if waitErr != nil {
		t.Fatalf("interactive Bash failed: %v; output=%q", waitErr, output.String())
	}
	return output.String()
}
