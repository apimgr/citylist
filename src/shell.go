package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var serverShells = []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "powershell", "pwsh"}

func printShellHelp() {
	printShellHelpTo(os.Stdout)
}

func printShellHelpTo(output io.Writer) {
	_, _ = fmt.Fprintf(output, "Usage: citylist --shell {completions|init} [SHELL]\nSupported shells: %s\n", strings.Join(serverShells, ", "))
}

func runServerShell(output io.Writer, action string, args []string) error {
	shell := detectServerShell()
	if len(args) > 0 {
		shell = normalizeServerShell(args[0])
	}
	if action == "help" || action == "--help" || action == "-h" {
		printShellHelpTo(output)
		return nil
	}
	if !containsServerShell(shell) {
		return fmt.Errorf("unsupported shell %q; supported shells: %s", shell, strings.Join(serverShells, ", "))
	}
	binary := filepath.Base(os.Args[0])
	switch action {
	case "completions":
		_, err := io.WriteString(output, serverCompletion(binary, shell))
		return err
	case "init":
		var line string
		switch shell {
		case "bash", "zsh":
			line = fmt.Sprintf("source <(%s --shell completions %s)", binary, shell)
		case "fish":
			line = fmt.Sprintf("%s --shell completions fish | source", binary)
		case "powershell", "pwsh":
			line = fmt.Sprintf("Invoke-Expression (& %s --shell completions powershell)", binary)
		default:
			line = fmt.Sprintf("eval \"$(%s --shell completions %s)\"", binary, shell)
		}
		_, err := fmt.Fprintln(output, line)
		return err
	default:
		return fmt.Errorf("unsupported --shell action %q; use completions or init", action)
	}
}

func detectServerShell() string {
	if shell := normalizeServerShell(os.Getenv("SHELL")); containsServerShell(shell) {
		return shell
	}
	if os.Getenv("PSModulePath") != "" {
		return "powershell"
	}
	return "bash"
}

func normalizeServerShell(shell string) string {
	shell = strings.ToLower(filepath.Base(strings.TrimSpace(shell)))
	if shell == "powershell.exe" {
		return "powershell"
	}
	return shell
}

func containsServerShell(shell string) bool {
	for _, candidate := range serverShells {
		if candidate == shell {
			return true
		}
	}
	return false
}

func serverCompletion(binary, shell string) string {
	commands := "--help --version --status --shell --mode --config --data --cache --log --backup --pid --address --port --baseurl --service --maintenance --update --i2p --daemon --debug --color --lang"
	switch shell {
	case "fish":
		return fmt.Sprintf("complete -c %s -f -a '%s'\n", binary, commands)
	case "powershell", "pwsh":
		return fmt.Sprintf("Register-ArgumentCompleter -Native -CommandName %s -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", binary, commands)
	case "zsh":
		return fmt.Sprintf("#compdef %s\n_arguments '*: :(%s)'\n", binary, commands)
	default:
		return fmt.Sprintf("complete -W '%s' %s\n", commands, binary)
	}
}
