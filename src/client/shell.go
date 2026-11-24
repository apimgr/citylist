package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var supportedShells = []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "powershell", "pwsh"}

func requestedShellHelp(args []string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--shell" && (args[i+1] == "--help" || args[i+1] == "-h" || args[i+1] == "help") {
			return "shell", true
		}
	}
	return "", false
}

func runShellAction(output, errorOutput io.Writer, binaryName, action string, args []string, lang string) int {
	if len(args) > 1 {
		_, _ = fmt.Fprintln(errorOutput, clientText(lang, "shell_arguments", "--shell accepts an action and optional shell name"))
		return 2
	}
	shell := detectShell()
	if len(args) == 1 {
		shell = strings.ToLower(args[0])
	}
	if !contains(supportedShells, shell) {
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(lang, "shell_unsupported", "unsupported shell {shell}; supported shells: {shells}", map[string]string{"shell": shell, "shells": strings.Join(supportedShells, ", ")}))
		return 2
	}
	switch action {
	case "completions":
		_, _ = io.WriteString(output, completionScript(shell, binaryName, lang))
	case "init":
		_, _ = fmt.Fprintln(output, initCommand(shell, binaryName))
	default:
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(lang, "shell_action", "unsupported --shell action {action}; use completions or init", map[string]string{"action": action}))
		return 2
	}
	return 0
}

func detectShell() string {
	if strings.TrimSpace(os.Getenv("PSModulePath")) != "" {
		return "powershell"
	}
	shell := strings.ToLower(filepath.Base(os.Getenv("SHELL")))
	if contains(supportedShells, shell) {
		return shell
	}
	return "bash"
}

func printShellHelp(output io.Writer, binaryName, lang string) {
	_, _ = fmt.Fprintln(output, clientTextFormat(lang, "shell_help", "Usage: {binary} --shell {completions|init} [SHELL]\nSupported shells: {shells}", map[string]string{"binary": binaryName, "shells": strings.Join(supportedShells, ", ")}))
}

func completionScript(shell, binaryName, lang string) string {
	commands := "city zipcode search zip-search country cities stats random nearest nearby zip-state zip-nearby"
	serverURL := clientText(lang, "completion_server", "Server URL")
	apiToken := clientText(lang, "completion_token", "API token")
	outputFormat := clientText(lang, "completion_output", "Output format")
	switch shell {
	case "bash", "sh", "dash", "ksh":
		return fmt.Sprintf("_%s_complete() {\n  COMPREPLY=( $(compgen -W '%s --server --token --token-file --config --output --lang --color --debug --version --help' -- \"${COMP_WORDS[COMP_CWORD]}\") )\n}\ncomplete -F _%s_complete %s\n", strings.ReplaceAll(binaryName, "-", "_"), commands, strings.ReplaceAll(binaryName, "-", "_"), binaryName)
	case "zsh":
		return fmt.Sprintf("#compdef %s\n_arguments '1:command:(%s)' '*:argument:' '--server[%s]:URL:' '--token[%s]:TOKEN:' '--output[%s]:FORMAT:(json table plain)' '--lang[%s]:CODE:'\n", binaryName, commands, serverURL, apiToken, outputFormat, clientText(lang, "completion_language", "Output language"))
	case "fish":
		var builder strings.Builder
		for _, command := range strings.Fields(commands) {
			fmt.Fprintf(&builder, "complete -c %s -f -n '__fish_use_subcommand' -a %s\n", binaryName, command)
		}
		fmt.Fprintf(&builder, "complete -c %s -l server -r -d '%s'\ncomplete -c %s -l token -r -d '%s'\ncomplete -c %s -l output -r -d '%s'\n", binaryName, serverURL, binaryName, apiToken, binaryName, outputFormat)
		return builder.String()
	case "powershell", "pwsh":
		return fmt.Sprintf("Register-ArgumentCompleter -Native -CommandName %s -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", binaryName, commands)
	default:
		return ""
	}
}

func initCommand(shell, binaryName string) string {
	switch shell {
	case "bash", "zsh":
		return fmt.Sprintf("source <(%s --shell completions %s)", binaryName, shell)
	case "fish":
		return fmt.Sprintf("%s --shell completions fish | source", binaryName)
	case "powershell", "pwsh":
		return fmt.Sprintf("Invoke-Expression (& %s --shell completions powershell)", binaryName)
	default:
		return fmt.Sprintf("eval \"$(%s --shell completions %s)\"", binaryName, shell)
	}
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
