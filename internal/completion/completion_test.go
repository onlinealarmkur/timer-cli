package completion

import (
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/onlinealarmkur/timer-cli/internal/cliinfo"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
)

type completionStateTest struct {
	name         string
	words        []string
	current      int
	wantContains []string
	wantAbsent   []string
	wantExact    []string
	exact        bool
}

var completionStateTests = []completionStateTest{
	{
		name: "first argument", words: []string{"timer-cli", ""}, current: 1,
		wantContains: []string{"version", "completion", "10m", "01:30", "5minutes", "5Minuten", "--title", "--quiet"},
	},
	{
		name: "separate title value", words: []string{"timer-cli", "--title", ""}, current: 2,
		wantExact: []string{}, exact: true,
	},
	{
		name: "after separate title value", words: []string{"timer-cli", "--title", "Tea", ""}, current: 3,
		wantContains: []string{"10m", "--title", "--quiet"},
		wantAbsent:   []string{"version", "completion"},
	},
	{
		name: "after equals title value", words: []string{"timer-cli", "--title=Tea", ""}, current: 2,
		wantContains: []string{"10m", "--title", "--quiet"},
		wantAbsent:   []string{"version", "completion"},
	},
	{
		name: "inside equals title value", words: []string{"timer-cli", "--title="}, current: 1,
		wantExact: []string{}, exact: true,
	},
	{
		name: "separate language value", words: []string{"timer-cli", "--lang", ""}, current: 2,
		wantExact: []string{"auto", "en", "es"}, exact: true,
	},
	{
		name: "equals language value", words: []string{"timer-cli", "--lang="}, current: 1,
		wantExact: []string{"--lang=auto", "--lang=en", "--lang=es"}, exact: true,
	},
	{
		name: "after global language", words: []string{"timer-cli", "--lang", "es", ""}, current: 3,
		wantContains: []string{"version", "completion", "10m", "--title", "--lang"},
	},
	{
		name: "after duration", words: []string{"timer-cli", "10m", ""}, current: 2,
		wantContains: []string{"--title", "--quiet"},
		wantAbsent: []string{
			"10m", "90s", "5minutes", "5minutos", "2heures", "5Minuten", "5minuti", "2uren",
			"version", "completion",
		},
	},
	{
		name: "after natural duration", words: []string{"timer-cli", "5", "minutes", ""}, current: 3,
		wantContains: []string{"--title", "--quiet"},
		wantAbsent:   []string{"10m", "5minutes", "version", "completion"},
	},
	{
		name: "loop excludes json", words: []string{"timer-cli", "10m", "--loop", ""}, current: 3,
		wantContains: []string{"--title", "--quiet", "--final-only"},
		wantAbsent:   []string{"--json"},
	},
	{
		name: "json excludes loop and other output modes", words: []string{"timer-cli", "10m", "--json", ""}, current: 3,
		wantContains: []string{"--title", "--no-bell"},
		wantAbsent:   []string{"--loop", "--quiet", "-q", "--final-only"},
	},
	{
		name: "quiet excludes other output modes", words: []string{"timer-cli", "10m", "-q", ""}, current: 3,
		wantContains: []string{"--loop", "--title"},
		wantAbsent:   []string{"--final-only", "--json"},
	},
	{
		name: "final only excludes other output modes", words: []string{"timer-cli", "--final-only", "10m", ""}, current: 3,
		wantContains: []string{"--loop", "--title"},
		wantAbsent:   []string{"--quiet", "-q", "--json"},
	},
	{
		name: "completion shell", words: []string{"timer-cli", "completion", ""}, current: 2,
		wantExact: []string{"bash", "zsh", "fish"}, exact: true,
	},
	{
		name: "language before completion", words: []string{"timer-cli", "--lang", "es", "completion", ""}, current: 4,
		wantExact: []string{"bash", "zsh", "fish"}, exact: true,
	},
	{
		name: "language after completion", words: []string{"timer-cli", "completion", "--lang", "es", ""}, current: 4,
		wantExact: []string{"bash", "zsh", "fish"}, exact: true,
	},
	{
		name: "equals language after completion", words: []string{"timer-cli", "completion", "--lang=es", ""}, current: 3,
		wantExact: []string{"bash", "zsh", "fish"}, exact: true,
	},
	{
		name: "after version", words: []string{"timer-cli", "version", ""}, current: 2,
		wantExact: []string{"--lang"}, exact: true,
	},
	{
		name: "after completed completion", words: []string{"timer-cli", "completion", "bash", ""}, current: 3,
		wantExact: []string{"--lang"}, exact: true,
	},
	{
		name: "after help", words: []string{"timer-cli", "--help", ""}, current: 2,
		wantExact: []string{"--lang"}, exact: true,
	},
	{
		name: "language value after version", words: []string{"timer-cli", "version", "--lang", ""}, current: 3,
		wantExact: []string{"auto", "en", "es"}, exact: true,
	},
	{
		name: "equals language after completed completion", words: []string{"timer-cli", "completion", "fish", "--lang="}, current: 3,
		wantExact: []string{"--lang=auto", "--lang=en", "--lang=es"}, exact: true,
	},
}

func TestShellCompletionsFollowParserState(t *testing.T) {
	candidateFunctions := []struct {
		name       string
		candidates func(*testing.T, []completionStateTest) [][]string
	}{
		{name: "bash", candidates: bashBatchCandidates},
		{name: "zsh", candidates: zshBatchCandidates},
		{name: "fish", candidates: fishBatchCandidates},
	}
	for _, shell := range candidateFunctions {
		t.Run(shell.name, func(t *testing.T) {
			results := shell.candidates(t, completionStateTests)
			if len(results) != len(completionStateTests) {
				t.Fatalf("collected %d completion results, want %d", len(results), len(completionStateTests))
			}
			for index, test := range completionStateTests {
				t.Run(test.name, func(t *testing.T) {
					assertCompletionState(t, results[index], test)
				})
			}
		})
	}
}

func TestShellCompletionCleanStart(t *testing.T) {
	test := completionStateTests[0]
	candidateFunctions := []struct {
		name       string
		candidates func(*testing.T, completionStateTest) []string
	}{
		{name: "bash", candidates: bashCandidates},
		{name: "zsh", candidates: zshCandidates},
		{name: "fish", candidates: fishCandidates},
	}
	for _, shell := range candidateFunctions {
		t.Run(shell.name, func(t *testing.T) {
			assertCompletionState(t, shell.candidates(t, test), test)
		})
	}
}

func assertCompletionState(t *testing.T, got []string, test completionStateTest) {
	t.Helper()
	if test.exact {
		want := append([]string(nil), test.wantExact...)
		sort.Strings(got)
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
	}
	set := make(map[string]bool, len(got))
	for _, candidate := range got {
		set[candidate] = true
	}
	for _, want := range test.wantContains {
		if !set[want] {
			t.Errorf("candidates %v omit %q", got, want)
		}
	}
	for _, unwanted := range test.wantAbsent {
		if set[unwanted] {
			t.Errorf("candidates %v unexpectedly include %q", got, unwanted)
		}
	}
}

const (
	completionBatchBegin     = "__TIMER_CLI_BATCH_BEGIN__"
	completionBatchCandidate = "__TIMER_CLI_BATCH_CANDIDATE__"
	completionBatchEnd       = "__TIMER_CLI_BATCH_END__"
)

func parseCompletionBatch(
	output []byte,
	tests []completionStateTest,
	decodeCandidate func(string) []string,
) ([][]string, error) {
	records := strings.Split(string(output), "\x00")
	results := make([][]string, len(tests))
	recordIndex := 0
	for testIndex, test := range tests {
		begin := fmt.Sprintf("%s%d", completionBatchBegin, testIndex)
		if recordIndex >= len(records) {
			return nil, fmt.Errorf("scenario %q: missing begin marker", test.name)
		}
		if records[recordIndex] != begin {
			return nil, fmt.Errorf("scenario %q: begin marker = %q, want %q", test.name, records[recordIndex], begin)
		}
		recordIndex++

		end := fmt.Sprintf("%s%d", completionBatchEnd, testIndex)
		for {
			if recordIndex >= len(records) {
				return nil, fmt.Errorf("scenario %q: missing end marker %q", test.name, end)
			}
			record := records[recordIndex]
			recordIndex++
			if record == end {
				break
			}
			if !strings.HasPrefix(record, completionBatchCandidate) {
				return nil, fmt.Errorf("scenario %q: unexpected framed record %q", test.name, record)
			}
			results[testIndex] = append(
				results[testIndex],
				decodeCandidate(strings.TrimPrefix(record, completionBatchCandidate))...,
			)
		}
	}
	for _, record := range records[recordIndex:] {
		if record != "" {
			return nil, fmt.Errorf("after scenario %q: unexpected trailing record %q", tests[len(tests)-1].name, record)
		}
	}
	return results, nil
}

func bashBatchCandidates(t *testing.T, tests []completionStateTest) [][]string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	script, err := ScriptFor("bash", localize.English)
	if err != nil {
		t.Fatal(err)
	}
	var fixture strings.Builder
	fixture.WriteString("\n")
	for index, test := range tests {
		fmt.Fprintf(&fixture, "printf '%s%d\\0'\n", completionBatchBegin, index)
		fixture.WriteString("COMP_WORDS=(")
		for _, word := range test.words {
			fmt.Fprintf(&fixture, " %q", word)
		}
		fixture.WriteString(" )\n")
		fmt.Fprintf(&fixture, "COMP_CWORD=%d\n", test.current)
		fixture.WriteString("COMPREPLY=()\n_timer_cli\n")
		fmt.Fprintf(
			&fixture,
			"for reply in \"${COMPREPLY[@]}\"; do printf '%s%%s\\0' \"$reply\"; done\n",
			completionBatchCandidate,
		)
		fmt.Fprintf(&fixture, "printf '%s%d\\0'\n", completionBatchEnd, index)
	}

	output, err := exec.Command(bash, "-c", script+fixture.String()).CombinedOutput()
	results, parseErr := parseCompletionBatch(output, tests, strings.Fields)
	if parseErr != nil {
		t.Fatalf("parse Bash completion batch: %v; process error=%v; output=%q", parseErr, err, output)
	}
	if err != nil {
		t.Fatalf("execute Bash completion batch after %d scenarios: %v: %q", len(tests), err, output)
	}
	return results
}

func bashCandidates(t *testing.T, test completionStateTest) []string {
	t.Helper()
	return bashBatchCandidates(t, []completionStateTest{test})[0]
}

func fishBatchCandidates(t *testing.T, tests []completionStateTest) [][]string {
	t.Helper()
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed; CI installs it in the bounded Linux quality job")
	}
	script, err := ScriptFor("fish", localize.English)
	if err != nil {
		t.Fatal(err)
	}
	var fixture strings.Builder
	fixture.WriteString(script)
	fixture.WriteString("\n")
	for index, test := range tests {
		fmt.Fprintf(&fixture, "printf '%s%d\\0'\n", completionBatchBegin, index)
		commandLine := strings.Join(test.words, " ")
		writeFishCompletionProbe(&fixture, commandLine)
		// Fish only offers option names after a dash prefix. Probe that
		// namespace separately so this state test covers arguments and options.
		if fishOptionProbeRequired(test) {
			optionWords := append([]string(nil), test.words...)
			optionWords[test.current] = "-"
			writeFishCompletionProbe(&fixture, strings.Join(optionWords, " "))
		}
		fmt.Fprintf(&fixture, "printf '%s%d\\0'\n", completionBatchEnd, index)
	}
	output, err := exec.Command(fish, "-c", fixture.String()).CombinedOutput()
	results, parseErr := parseCompletionBatch(output, tests, func(line string) []string {
		if line == "" {
			return nil
		}
		candidate, _, _ := strings.Cut(line, "\t")
		return []string{candidate}
	})
	if parseErr != nil {
		t.Fatalf("parse fish completion batch: %v; process error=%v; output=%q", parseErr, err, output)
	}
	if err != nil {
		t.Fatalf("execute fish completion batch after %d scenarios: %v: %q", len(tests), err, output)
	}
	return results
}

func writeFishCompletionProbe(fixture *strings.Builder, commandLine string) {
	fmt.Fprintf(
		fixture,
		"complete -C '%s' | while read -l reply; printf '%s%%s\\0' \"$reply\"; end\n",
		fishEscape(commandLine), completionBatchCandidate,
	)
}

func fishOptionProbeRequired(test completionStateTest) bool {
	if test.current < 0 || test.current >= len(test.words) || test.words[test.current] != "" {
		return false
	}
	for _, candidates := range [][]string{test.wantContains, test.wantAbsent, test.wantExact} {
		for _, candidate := range candidates {
			if strings.HasPrefix(candidate, "-") {
				return true
			}
		}
	}
	return false
}

func fishCandidates(t *testing.T, test completionStateTest) []string {
	t.Helper()
	return fishBatchCandidates(t, []completionStateTest{test})[0]
}

func zshCandidates(t *testing.T, test completionStateTest) []string {
	t.Helper()
	return zshBatchCandidatesFor(t, localize.English, []completionStateTest{test})[0]
}

func zshBatchCandidates(t *testing.T, tests []completionStateTest) [][]string {
	t.Helper()
	return zshBatchCandidatesFor(t, localize.English, tests)
}

func zshBatchCandidatesFor(t *testing.T, language localize.Language, tests []completionStateTest) [][]string {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed; CI installs it in the bounded Linux quality job")
	}
	script, err := ScriptFor("zsh", language)
	if err != nil {
		t.Fatal(err)
	}

	var fixture strings.Builder
	fixture.WriteString(`
function compdef { : }
typeset -g _timer_cli_test_prefix=''
function compset {
  if [[ "$1" == -P && "${words[CURRENT]}" == "$2"* ]]; then
    _timer_cli_test_prefix="$2"
    return 0
  fi
  return 1
}
function _timer_cli_test_emit {
  printf '%s%s\0' '` + completionBatchCandidate + `' "$1"
}
function _describe {
  local values_name="$2"
  local value
  for value in "${(@P)values_name}"; do
    _timer_cli_test_emit "$value"
  done
}
function _values {
  shift
  local value
  for value in "$@"; do
    _timer_cli_test_emit "${_timer_cli_test_prefix}${value}"
  done
}
function _message { : }
`)
	fixture.WriteString(script)
	fixture.WriteByte('\n')
	for index, test := range tests {
		fmt.Fprintf(&fixture, "printf '%%s\\0' '%s%d'\n", completionBatchBegin, index)
		fixture.WriteString("words=(")
		for _, word := range test.words {
			fmt.Fprintf(&fixture, " %s", zshQuote(word))
		}
		fixture.WriteString(" )\n")
		fmt.Fprintf(&fixture, "CURRENT=%d\n", test.current+1)
		fixture.WriteString("_timer_cli_test_prefix=''\n_timer_cli\n")
		fmt.Fprintf(&fixture, "printf '%%s\\0' '%s%d'\n", completionBatchEnd, index)
	}

	output, err := exec.Command(zsh, "-f", "-c", fixture.String()).CombinedOutput()
	results, parseErr := parseCompletionBatch(output, tests, decodeZshCandidate)
	if parseErr != nil {
		t.Fatalf("parse zsh completion batch: %v; process error=%v; output=%q", parseErr, err, output)
	}
	if err != nil {
		t.Fatalf("execute zsh completion batch after %d scenarios: %v: %q", len(tests), err, output)
	}
	return results
}

func decodeZshCandidate(record string) []string {
	end := len(record)
	escaped := false
	for index := 0; index < len(record); index++ {
		switch {
		case escaped:
			escaped = false
		case record[index] == '\\':
			escaped = true
		case record[index] == ':':
			end = index
			index = len(record)
		}
	}

	var value strings.Builder
	for index := 0; index < end; index++ {
		if record[index] == '\\' && index+1 < end {
			index++
		}
		value.WriteByte(record[index])
	}
	return []string{value.String()}
}

func TestScriptsFollowCLIContract(t *testing.T) {
	t.Parallel()
	legacyRegistrations := []string{
		"complete -F _timer timer\n",
		"#compdef timer\n",
		"_timer()",
		"complete -c timer ",
	}

	for _, shell := range cliinfo.Shells() {
		shell := shell
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			script, err := ScriptFor(shell, localize.English)
			if err != nil {
				t.Fatal(err)
			}
			again, err := ScriptFor(shell, localize.English)
			if err != nil || script != again {
				t.Fatalf("ScriptFor(%q, English) is not deterministic", shell)
			}
			if !strings.Contains(script, cliinfo.ProgramName) {
				t.Fatalf("ScriptFor(%q, English) omits public command name", shell)
			}
			for _, legacy := range legacyRegistrations {
				if strings.Contains(script, legacy) {
					t.Fatalf("ScriptFor(%q, English) contains legacy registration %q", shell, legacy)
				}
			}

			for _, command := range cliinfo.Commands() {
				if !containsCommand(script, shell, command.Name) {
					t.Errorf("ScriptFor(%q, English) omits command %q", shell, command.Name)
				}
			}
			for _, option := range cliinfo.Options() {
				if !containsOption(script, shell, option) {
					t.Errorf("ScriptFor(%q, English) omits option --%s", shell, option.Long)
				}
			}
			for _, example := range cliinfo.DurationExamples() {
				want := example
				if shell == "zsh" {
					want = strings.ReplaceAll(want, ":", `\:`)
				}
				if !strings.Contains(script, want) {
					t.Errorf("ScriptFor(%q, English) omits duration example %q", shell, example)
				}
			}
			if !containsCompletionShellState(script, shell) {
				t.Errorf("ScriptFor(%q, English) does not complete all shells after completion", shell)
			}
		})
	}
}

func TestCompletionScriptSyntax(t *testing.T) {
	t.Parallel()
	for _, shell := range cliinfo.Shells() {
		shell := shell
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			executable, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not installed; CI installs zsh and fish in the bounded Linux quality job", shell)
			}
			for _, test := range []struct {
				name     string
				language localize.Language
			}{
				{name: "English", language: localize.English},
				{name: "Spanish", language: localize.Spanish},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					script, err := ScriptFor(shell, test.language)
					if err != nil {
						t.Fatal(err)
					}
					command := exec.Command(executable, "-n")
					command.Stdin = strings.NewReader(script)
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("%s syntax check: %v: %s", shell, err, output)
					}
				})
			}
		})
	}
}

func TestZshCompletionRegistersWithCompinit(t *testing.T) {
	t.Parallel()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed; CI installs it in the bounded Linux quality job")
	}
	script, err := ScriptFor("zsh", localize.English)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(zsh, "-f", "-c", `
autoload -Uz compinit
compinit -i -d /dev/null
source /dev/stdin
registered="${_comps[timer-cli]}"
[[ "$registered" == _timer_cli ]]
`)
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("register zsh completion with compinit: %v: %s", err, output)
	}
}

func TestSpanishZshCompletionHandlesLocalizedDescriptions(t *testing.T) {
	tests := []struct {
		name  string
		words []string
		want  []string
	}{
		{
			name:  "first argument",
			words: []string{"timer-cli", ""},
			want:  []string{"version", "10m", "--message", "--quiet"},
		},
		{
			name:  "options after duration",
			words: []string{"timer-cli", "10m", ""},
			want:  []string{"--message", "--quiet", "--lang"},
		},
	}
	stateTests := make([]completionStateTest, len(tests))
	for index, test := range tests {
		stateTests[index] = completionStateTest{name: test.name, words: test.words, current: len(test.words) - 1}
	}
	results := zshBatchCandidatesFor(t, localize.Spanish, stateTests)
	if len(results) != len(tests) {
		t.Fatalf("collected %d Spanish zsh completion results, want %d", len(results), len(tests))
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := results[index]
			set := make(map[string]bool, len(candidates))
			for _, candidate := range candidates {
				set[candidate] = true
			}
			for _, want := range test.want {
				if !set[want] {
					t.Errorf("Spanish zsh candidates %v omit %q", candidates, want)
				}
			}
		})
	}
}

func TestBashAndZshFirstArgumentSetsIncludeOptions(t *testing.T) {
	t.Parallel()
	first := strings.Join(firstArgumentWords(), " ")
	bash, err := ScriptFor("bash", localize.English)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bash, `compgen -W "`+first+`"`) {
		t.Fatalf("bash first-argument set is incomplete: %q", bash)
	}

	zsh, err := ScriptFor("zsh", localize.English)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range firstArgumentWords() {
		escaped := strings.ReplaceAll(strings.ReplaceAll(word, `\`, `\\`), ":", `\:`)
		if !strings.Contains(zsh, "'"+escaped+":") {
			t.Fatalf("zsh first-argument set omits %q: %q", word, zsh)
		}
	}
	if !strings.Contains(zsh, `candidates=("${commands[@]}" "${durations[@]}" "${filtered_options[@]}")`) {
		t.Fatalf("zsh does not combine every first-argument candidate set: %q", zsh)
	}
}

func TestUnsupportedShell(t *testing.T) {
	t.Parallel()
	if script, err := ScriptFor("powershell", localize.English); err == nil || script != "" {
		t.Fatalf("ScriptFor(powershell, English) = %q, %v", script, err)
	}
}

func TestLocalizedCompletionDescriptionsAndLanguageChoices(t *testing.T) {
	t.Parallel()
	zsh, err := ScriptFor("zsh", localize.Spanish)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"duración, comando u opción",
		"Define el idioma de la interfaz",
		"expect_label='TEXTO'",
		"_values 'IDIOMA' auto en es",
	} {
		if !strings.Contains(zsh, want) {
			t.Errorf("Spanish zsh completion omits %q: %q", want, zsh)
		}
	}

	fish, err := ScriptFor("fish", localize.Spanish)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Duración de ejemplo", "Muestra un título opcional", "-l lang -x -a 'auto en es'"} {
		if !strings.Contains(fish, want) {
			t.Errorf("Spanish fish completion omits %q: %q", want, fish)
		}
	}

	if _, err := ScriptFor("powershell", localize.Spanish); err == nil || !strings.Contains(err.Error(), "shell no compatible") {
		t.Fatalf("Spanish unsupported-shell error = %v", err)
	}
}

func containsCommand(script, shell, command string) bool {
	if shell == "fish" {
		return strings.Contains(script, "-a "+command+" ")
	}
	return strings.Contains(script, command)
}

func containsOption(script, shell string, option cliinfo.Option) bool {
	if shell == "fish" {
		line := lineContaining(script, "-l "+option.Long)
		if line == "" || option.Short != "" && !strings.Contains(line, "-s "+option.Short) {
			return false
		}
		return !option.TakesValue || strings.Contains(line, " -x")
	}
	line := lineContaining(script, "--"+option.Long)
	if shell == "zsh" {
		line = zshOptionLine(script, option.Long)
	}
	if line == "" {
		return false
	}
	if option.Short != "" && !strings.Contains(line, "-"+option.Short) {
		return false
	}
	if shell == "zsh" && option.TakesValue {
		return strings.Contains(script, "--"+option.Long+")")
	}
	return true
}

func zshOptionLine(script, long string) string {
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "'--"+long+":") {
			return line
		}
	}
	return ""
}

func containsCompletionShellState(script, shell string) bool {
	shells := strings.Join(cliinfo.Shells(), " ")
	switch shell {
	case "bash":
		return strings.Contains(script, `"${COMP_WORDS[command_index]}" == completion`) &&
			strings.Contains(script, `compgen -W "`+shells+`"`)
	case "zsh":
		return strings.Contains(script, "completion_seen=1") &&
			strings.Contains(script, "_values 'shell' "+shells)
	case "fish":
		return strings.Contains(script, "__timer_cli_accepts shell") &&
			strings.Contains(script, "-a '"+shells+"'")
	default:
		return false
	}
}

func lineContaining(text, fragment string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, fragment) {
			return line
		}
	}
	return ""
}
