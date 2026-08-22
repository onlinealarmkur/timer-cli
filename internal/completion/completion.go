// Package completion generates shell completion scripts without side effects.
package completion

import (
	"errors"
	"fmt"
	"strings"

	"github.com/onlinealarmkur/timer-cli/internal/cliinfo"
	"github.com/onlinealarmkur/timer-cli/internal/localize"
)

// ScriptFor returns a completion script with localized descriptions.
func ScriptFor(shell string, language localize.Language) (string, error) {
	switch strings.ToLower(shell) {
	case "bash":
		return bashScript(), nil
	case "zsh":
		return zshScript(language), nil
	case "fish":
		return fishScript(language), nil
	default:
		return "", errors.New(localize.Format(language, localize.ErrorUnsupportedShellFormat, shell, strings.Join(cliinfo.Shells(), ", ")))
	}
}

func commandNames() []string {
	commands := cliinfo.Commands()
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	return names
}

func flagNames() []string {
	options := cliinfo.Options()
	names := make([]string, 0, len(options)*2)
	for _, option := range options {
		names = append(names, "--"+option.Long)
		if option.Short != "" {
			names = append(names, "-"+option.Short)
		}
	}
	return names
}

func valueFlagNames() []string {
	options := cliinfo.Options()
	names := make([]string, 0, len(options))
	for _, option := range options {
		if option.TakesValue {
			names = append(names, "--"+option.Long)
		}
	}
	return names
}

func valueFlagPatterns() []string {
	names := valueFlagNames()
	patterns := make([]string, 0, len(names))
	for _, name := range names {
		patterns = append(patterns, name+"=*")
	}
	return patterns
}

func firstArgumentWords() []string {
	words := commandNames()
	words = append(words, cliinfo.DurationExamples()...)
	return append(words, flagNames()...)
}

func bashOptionMetadata() (canonical, conflicts string) {
	var canonicalCases, conflictCases strings.Builder
	for _, option := range cliinfo.Options() {
		aliases := "--" + option.Long
		if option.Short != "" {
			aliases += "|-" + option.Short
		}
		fmt.Fprintf(&canonicalCases, "    %s) printf '%%s' --%s ;;\n", aliases, option.Long)
		if len(option.Conflicts) > 0 {
			fmt.Fprintf(&conflictCases, "    --%s) printf '%%s\\n'", option.Long)
			for _, conflict := range option.Conflicts {
				fmt.Fprintf(&conflictCases, " --%s", conflict)
			}
			conflictCases.WriteString(" ;;\n")
		}
	}
	return canonicalCases.String(), conflictCases.String()
}

func zshOptionMetadata() (aliases, conflicts string) {
	var aliasEntries, conflictEntries strings.Builder
	for _, option := range cliinfo.Options() {
		canonical := "--" + option.Long
		fmt.Fprintf(&aliasEntries, "    %s %s\n", zshQuote(canonical), zshQuote(canonical))
		if option.Short != "" {
			fmt.Fprintf(&aliasEntries, "    %s %s\n", zshQuote("-"+option.Short), zshQuote(canonical))
		}
		if len(option.Conflicts) > 0 {
			values := make([]string, 0, len(option.Conflicts))
			for _, conflict := range option.Conflicts {
				values = append(values, "--"+conflict)
			}
			fmt.Fprintf(&conflictEntries, "    %s %s\n", zshQuote(canonical), zshQuote(strings.Join(values, " ")))
		}
	}
	return aliasEntries.String(), conflictEntries.String()
}

func fishOptionMetadata() (canonical, conflicts string) {
	var canonicalCases, conflictCases strings.Builder
	for _, option := range cliinfo.Options() {
		fmt.Fprintf(&canonicalCases, "    case --%s", option.Long)
		if option.Short != "" {
			fmt.Fprintf(&canonicalCases, " -%s", option.Short)
		}
		fmt.Fprintf(&canonicalCases, "\n      echo --%s\n", option.Long)
		if len(option.Conflicts) > 0 {
			fmt.Fprintf(&conflictCases, "    case --%s\n      printf '%%s\\n'", option.Long)
			for _, conflict := range option.Conflicts {
				fmt.Fprintf(&conflictCases, " --%s", conflict)
			}
			conflictCases.WriteByte('\n')
		}
	}
	return canonicalCases.String(), conflictCases.String()
}

func bashScript() string {
	canonicalCases, conflictCases := bashOptionMetadata()
	return fmt.Sprintf(`# bash completion for %[1]s
_timer_cli_takes_value() {
  case "$1" in
    %[6]s) return 0 ;;
  esac
  return 1
}

_timer_cli_canonical_option() {
  case "$1" in
%[11]s  esac
}

_timer_cli_conflicts() {
  case "$1" in
%[12]s  esac
}

_timer_cli() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local duration_seen=0
  local expect_value=0
  local terminal_seen=0
  local command_eligible=1
  local command_index=1
  local argument_index
  local completion_done=0
  local i word canonical candidate seen conflict
  local -i allowed
  local -a seen_options=() option_candidates=()
  COMPREPLY=()

  case "$cur" in
    --lang=*)
      local lang_value="${cur#--lang=}"
      COMPREPLY=( $(compgen -W "%[10]s" -- "$lang_value") )
      COMPREPLY=( "${COMPREPLY[@]/#/--lang=}" )
      return ;;
  esac
  if [[ "$COMP_CWORD" -gt 0 && "${COMP_WORDS[COMP_CWORD-1]}" == --lang ]]; then
    COMPREPLY=( $(compgen -W "%[10]s" -- "$cur") )
    return
  fi

  case "$cur" in
    %[7]s) return ;;
  esac

  if [[ "$COMP_CWORD" == 1 ]]; then
    COMPREPLY=( $(compgen -W "%[2]s" -- "$cur") )
    return
  fi

  while (( command_index < COMP_CWORD )); do
    word="${COMP_WORDS[command_index]}"
    if [[ "$word" == --lang ]]; then
      ((command_index += 2))
      continue
    fi
    if [[ "$word" == --lang=* ]]; then
      ((command_index++))
      continue
    fi
    break
  done

  if [[ "${COMP_WORDS[command_index]}" == %[8]s ]]; then
    COMPREPLY=( $(compgen -W "--lang" -- "$cur") )
    return
  fi
  if [[ "${COMP_WORDS[command_index]}" == %[5]s ]]; then
    argument_index=$((command_index + 1))
    while (( argument_index < COMP_CWORD )); do
      word="${COMP_WORDS[argument_index]}"
      if [[ "$word" == --lang ]]; then
        ((argument_index += 2))
        continue
      fi
      if [[ "$word" == --lang=* ]]; then
        ((argument_index++))
        continue
      fi
      if (( completion_done )); then
        return
      fi
      case " %[3]s " in
        *" $word "*) completion_done=1; ((argument_index++)); continue ;;
      esac
      return
    done
    if (( argument_index == COMP_CWORD )); then
      if (( completion_done )); then
        COMPREPLY=( $(compgen -W "--lang" -- "$cur") )
      else
        COMPREPLY=( $(compgen -W "%[3]s" -- "$cur") )
      fi
    fi
    return
  fi

  for ((i = 1; i < COMP_CWORD; i++)); do
    word="${COMP_WORDS[i]}"
    if (( expect_value )); then
      expect_value=0
      continue
    fi
    canonical="$(_timer_cli_canonical_option "${word%%=*}")"
    if [[ -n "$canonical" ]]; then
      seen_options+=("$canonical")
    fi
    if _timer_cli_takes_value "$word"; then
      expect_value=1
      if [[ "$word" != --lang ]]; then
        command_eligible=0
      fi
      continue
    fi
    case "$word" in
      --help|-h|--version) terminal_seen=1 ;;
      --lang=*) ;;
      %[7]s) command_eligible=0 ;;
      --*|-*) command_eligible=0 ;;
      *) duration_seen=1; command_eligible=0 ;;
    esac
  done

  if (( expect_value )); then
    return
  fi
  if (( terminal_seen )); then
    COMPREPLY=( $(compgen -W "--lang" -- "$cur") )
    return
  fi
  for candidate in %[4]s; do
    canonical="$(_timer_cli_canonical_option "$candidate")"
    allowed=1
    for seen in "${seen_options[@]}"; do
      for conflict in $(_timer_cli_conflicts "$seen"); do
        if [[ "$canonical" == "$conflict" ]]; then
          allowed=0
          break 2
        fi
      done
    done
    if (( allowed )); then
      option_candidates+=("$candidate")
    fi
  done
  if (( duration_seen )); then
    COMPREPLY=( $(compgen -W "${option_candidates[*]}" -- "$cur") )
    return
  fi
  if (( command_eligible )); then
    COMPREPLY=( $(compgen -W "%[2]s" -- "$cur") )
    return
  fi
  COMPREPLY=( $(compgen -W "%[9]s ${option_candidates[*]}" -- "$cur") )
}
complete -F _timer_cli %[1]s
`, cliinfo.ProgramName, strings.Join(firstArgumentWords(), " "), strings.Join(cliinfo.Shells(), " "), strings.Join(flagNames(), " "), cliinfo.CommandCompletion, strings.Join(valueFlagNames(), "|"), strings.Join(valueFlagPatterns(), "|"), cliinfo.CommandVersion, strings.Join(cliinfo.DurationExamples(), " "), strings.Join(localize.Choices(), " "), canonicalCases, conflictCases)
}

func zshScript(language localize.Language) string {
	optionAliases, optionConflicts := zshOptionMetadata()
	var script strings.Builder
	fmt.Fprintf(&script, "#compdef %s\n", cliinfo.ProgramName)
	script.WriteString("_timer_cli() {\n")
	script.WriteString("  local word expect_label option_name canonical candidate seen conflict\n")
	script.WriteString("  local -i i duration_seen=0 expect_value=0 expect_language=0 terminal_seen=0 allowed=1\n")
	script.WriteString("  local -i command_eligible=1 completion_seen=0 completion_done=0\n")
	script.WriteString("  local -a commands durations options filtered_options candidates seen_options\n")
	script.WriteString("  local -A option_aliases option_conflicts\n")
	script.WriteString("  option_aliases=(\n")
	script.WriteString(optionAliases)
	script.WriteString("  )\n  option_conflicts=(\n")
	script.WriteString(optionConflicts)
	script.WriteString("  )\n")
	script.WriteString("  commands=(\n")
	for _, command := range cliinfo.CommandsFor(language) {
		fmt.Fprintf(&script, "    %s\n", zshDescribeEntry(command.Name, command.Description))
	}
	script.WriteString("  )\n  durations=(\n")
	for _, example := range cliinfo.DurationExamples() {
		fmt.Fprintf(&script, "    %s\n", zshDescribeEntry(example, localize.Text(language, localize.CompletionExampleDurationDescription)))
	}
	script.WriteString("  )\n  options=(\n")
	for _, option := range cliinfo.OptionsFor(language) {
		fmt.Fprintf(&script, "    %s\n", zshDescribeEntry("--"+option.Long, option.Description))
		if option.Short != "" {
			fmt.Fprintf(&script, "    %s\n", zshDescribeEntry("-"+option.Short, option.Description))
		}
	}
	script.WriteString("  )\n\n")
	script.WriteString("  for (( i = 2; i < CURRENT; i++ )); do\n")
	script.WriteString("    word=\"${words[i]}\"\n")
	script.WriteString("    if (( expect_value )); then\n")
	script.WriteString("      expect_value=0\n      expect_language=0\n      expect_label=''\n      continue\n    fi\n")
	script.WriteString("    option_name=\"${word%%=*}\"\n")
	script.WriteString("    canonical=\"${option_aliases[$option_name]-}\"\n")
	script.WriteString("    [[ -n \"$canonical\" ]] && seen_options+=(\"$canonical\")\n")
	script.WriteString("    case \"$word\" in\n")
	fmt.Fprintf(&script, "      --title) expect_value=1; expect_label=%s; command_eligible=0 ;;\n",
		zshQuote(localize.Text(language, localize.TextValueLabel)))
	fmt.Fprintf(&script, "      --message) expect_value=1; expect_label=%s; command_eligible=0 ;;\n",
		zshQuote(localize.Text(language, localize.TextValueLabel)))
	script.WriteString("      --bell-count) expect_value=1; expect_label='N'; command_eligible=0 ;;\n")
	fmt.Fprintf(&script, "      --lang) expect_value=1; expect_language=1; expect_label=%s ;;\n",
		zshQuote(localize.Text(language, localize.LanguageValueLabel)))
	script.WriteString("      --title=*|--message=*|--bell-count=*) command_eligible=0 ;;\n")
	script.WriteString("      --lang=*) ;;\n")
	script.WriteString("      --help|-h|--version) terminal_seen=1 ;;\n")
	script.WriteString("      --*|-*) command_eligible=0 ;;\n")
	fmt.Fprintf(&script, "      %s)\n", cliinfo.CommandCompletion)
	script.WriteString("        if (( command_eligible && ! duration_seen )); then\n")
	script.WriteString("          completion_seen=1\n          command_eligible=0\n")
	script.WriteString("        elif (( completion_seen )); then\n          completion_done=1\n")
	script.WriteString("        else\n          duration_seen=1\n          command_eligible=0\n        fi\n        ;;\n")
	fmt.Fprintf(&script, "      %s)\n", cliinfo.CommandVersion)
	script.WriteString("        if (( command_eligible && ! duration_seen )); then\n")
	script.WriteString("          terminal_seen=1\n")
	script.WriteString("        elif (( completion_seen )); then\n          completion_done=1\n")
	script.WriteString("        else\n          duration_seen=1\n          command_eligible=0\n        fi\n        ;;\n")
	script.WriteString("      *)\n")
	script.WriteString("        if (( completion_seen )); then\n          completion_done=1\n")
	script.WriteString("        else\n          duration_seen=1\n          command_eligible=0\n        fi\n        ;;\n")
	script.WriteString("    esac\n  done\n\n")
	script.WriteString("  if (( expect_value )); then\n")
	fmt.Fprintf(&script, "    if (( expect_language )); then\n      _values %s %s\n",
		zshQuote(localize.Text(language, localize.LanguageValueLabel)), strings.Join(localize.Choices(), " "))
	script.WriteString("    else\n      _message \"$expect_label\"\n    fi\n    return\n  fi\n")
	script.WriteString("  if compset -P '--lang='; then\n")
	fmt.Fprintf(&script, "    _values %s %s\n", zshQuote(localize.Text(language, localize.LanguageValueLabel)), strings.Join(localize.Choices(), " "))
	script.WriteString("    return\n  fi\n")
	script.WriteString("  if (( terminal_seen || completion_done )); then\n")
	script.WriteString("    for candidate in \"${options[@]}\"; do\n")
	script.WriteString("      [[ \"${candidate%%:*}\" == --lang ]] && filtered_options+=(\"$candidate\")\n")
	script.WriteString("    done\n")
	fmt.Fprintf(&script, "    _describe %s filtered_options\n", zshQuote(localize.Text(language, localize.CompletionFirstArgumentDescription)))
	script.WriteString("    return\n  fi\n")
	script.WriteString("  if (( completion_seen )); then\n")
	fmt.Fprintf(&script, "    _values %s %s\n", zshQuote(localize.Text(language, localize.CompletionShellLabel)), strings.Join(cliinfo.Shells(), " "))
	script.WriteString("    return\n  fi\n")
	script.WriteString("  if [[ \"${words[CURRENT]}\" == --title=* || \"${words[CURRENT]}\" == --message=* || \"${words[CURRENT]}\" == --bell-count=* ]]; then\n")
	script.WriteString("    return\n  fi\n")
	script.WriteString("  filtered_options=()\n")
	script.WriteString("  for candidate in \"${options[@]}\"; do\n")
	script.WriteString("    option_name=\"${candidate%%:*}\"\n")
	script.WriteString("    canonical=\"${option_aliases[$option_name]-}\"\n")
	script.WriteString("    allowed=1\n")
	script.WriteString("    for seen in \"${seen_options[@]}\"; do\n")
	script.WriteString("      for conflict in ${(z)option_conflicts[$seen]}; do\n")
	script.WriteString("        if [[ \"$canonical\" == \"$conflict\" ]]; then\n          allowed=0\n          break 2\n        fi\n")
	script.WriteString("      done\n    done\n")
	script.WriteString("    (( allowed )) && filtered_options+=(\"$candidate\")\n")
	script.WriteString("  done\n")
	script.WriteString("  candidates=(\"${filtered_options[@]}\")\n")
	script.WriteString("  if (( ! duration_seen )); then\n")
	script.WriteString("    candidates=(\"${durations[@]}\" \"${filtered_options[@]}\")\n")
	script.WriteString("    if (( command_eligible )); then\n")
	script.WriteString("      candidates=(\"${commands[@]}\" \"${durations[@]}\" \"${filtered_options[@]}\")\n")
	script.WriteString("    fi\n  fi\n")
	fmt.Fprintf(&script, "  _describe %s candidates\n", zshQuote(localize.Text(language, localize.CompletionFirstArgumentDescription)))
	script.WriteString("}\n")
	fmt.Fprintf(&script, "compdef _timer_cli %s\n", cliinfo.ProgramName)
	return script.String()
}

func fishScript(language localize.Language) string {
	canonicalCases, conflictCases := fishOptionMetadata()
	var script strings.Builder
	script.WriteString("function __timer_cli_canonical_option --argument-names option\n  switch $option\n")
	script.WriteString(canonicalCases)
	script.WriteString("  end\nend\n\nfunction __timer_cli_conflicts --argument-names option\n  switch $option\n")
	script.WriteString(conflictCases)
	script.WriteString("  end\nend\n\n")
	script.WriteString(`function __timer_cli_accepts --argument-names query candidate
  set -l words (commandline -opc)
  set -e words[1]
  set -l duration_seen 0
  set -l expect_value 0
  set -l expect_language 0
  set -l terminal_seen 0
  set -l command_eligible 1
  set -l completion_seen 0
  set -l completion_done 0
  set -l seen_options

  for word in $words
    if test $expect_value -eq 1
      set expect_value 0
      set expect_language 0
      continue
    end
    set -l option_name (string split -m1 '=' -- $word)[1]
    set -l canonical (__timer_cli_canonical_option $option_name)
    test -n "$canonical"; and set -a seen_options $canonical
    if string match -q -- '--lang=*' $word
      continue
    end
    if string match -q -- '--title=*' $word; or string match -q -- '--message=*' $word; or string match -q -- '--bell-count=*' $word
      set command_eligible 0
      continue
    end
    switch $word
      case --title --message --bell-count
        set expect_value 1
        set command_eligible 0
      case --lang
        set expect_value 1
        set expect_language 1
      case --help -h --version
        set terminal_seen 1
      case '-*'
        set command_eligible 0
      case completion
        if test $command_eligible -eq 1; and test $duration_seen -eq 0
          set completion_seen 1
          set command_eligible 0
        else if test $completion_seen -eq 1
          set completion_done 1
        else
          set duration_seen 1
          set command_eligible 0
        end
      case version
        if test $command_eligible -eq 1; and test $duration_seen -eq 0
          set terminal_seen 1
        else if test $completion_seen -eq 1
          set completion_done 1
        else
          set duration_seen 1
          set command_eligible 0
        end
      case '*'
        if test $completion_seen -eq 1
          set completion_done 1
        else
          set duration_seen 1
          set command_eligible 0
        end
    end
  end

  switch $query
    case command
      test $expect_value -eq 0; and test $terminal_seen -eq 0; and test $completion_seen -eq 0; and test $duration_seen -eq 0; and test $command_eligible -eq 1
    case duration
      test $expect_value -eq 0; and test $terminal_seen -eq 0; and test $completion_seen -eq 0; and test $duration_seen -eq 0
    case option
      if test $expect_value -ne 0
        return 1
      end
      if test $terminal_seen -ne 0; or test $completion_done -ne 0
        test "$candidate" = --lang
        return $status
      end
      if test $completion_seen -ne 0
        return 1
      end
      set -l canonical (__timer_cli_canonical_option $candidate)
      for seen in $seen_options
        contains -- $canonical (__timer_cli_conflicts $seen); and return 1
      end
      return 0
    case language
      test $expect_value -eq 1; and test $expect_language -eq 1
    case shell
      test $expect_value -eq 0; and test $terminal_seen -eq 0; and test $completion_seen -eq 1; and test $completion_done -eq 0
  end
end

`)
	fmt.Fprintf(&script, "complete -c %s -f\n", cliinfo.ProgramName)
	for _, command := range cliinfo.CommandsFor(language) {
		fmt.Fprintf(&script, "complete -c %s -n '__timer_cli_accepts command' -a %s -d '%s'\n",
			cliinfo.ProgramName, command.Name, fishEscape(command.Description))
	}
	fmt.Fprintf(&script, "complete -c %s -n '__timer_cli_accepts duration' -a '%s' -d '%s'\n",
		cliinfo.ProgramName, strings.Join(cliinfo.DurationExamples(), " "), fishEscape(localize.Text(language, localize.CompletionExampleDurationDescription)))
	fmt.Fprintf(&script, "complete -c %s -n '__timer_cli_accepts shell' -a '%s' -d '%s'\n",
		cliinfo.ProgramName, strings.Join(cliinfo.Shells(), " "),
		fishEscape(localize.Text(language, localize.CompletionShellDescription)))
	for _, option := range cliinfo.OptionsFor(language) {
		fmt.Fprintf(&script, "complete -c %s -l %s", cliinfo.ProgramName, option.Long)
		if option.Short != "" {
			fmt.Fprintf(&script, " -s %s", option.Short)
		}
		if option.TakesValue {
			// -x both requires a value and disables fish's implicit filename
			// completions. None of timer-cli's option values are paths.
			script.WriteString(" -x")
		}
		if option.Long == "lang" {
			fmt.Fprintf(&script, " -a '%s'", strings.Join(localize.Choices(), " "))
		}
		condition := "__timer_cli_accepts option --" + option.Long
		if option.Long == "lang" {
			condition += "; or __timer_cli_accepts language"
		}
		fmt.Fprintf(&script, " -n '%s'", condition)
		fmt.Fprintf(&script, " -d '%s'\n", fishEscape(option.Description))
	}
	return script.String()
}

func zshDescribeEntry(value, description string) string {
	escape := func(text string) string {
		text = strings.ReplaceAll(text, `\`, `\\`)
		return strings.ReplaceAll(text, ":", `\:`)
	}
	return zshQuote(escape(value) + ":" + escape(description))
}

func zshQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func fishEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}
