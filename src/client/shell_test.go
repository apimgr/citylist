package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestShellHelpAndEnvironmentDetection(t *testing.T) {
	for _, value := range []string{"--help", "-h", "help"} {
		if command, ok := requestedShellHelp([]string{"--shell", value}); !ok || command != "shell" {
			t.Errorf("requestedShellHelp(%q) = %q, %t", value, command, ok)
		}
	}
	if _, ok := requestedShellHelp([]string{"--shell", "completions"}); ok {
		t.Fatal("completion request was treated as help")
	}
	t.Setenv("PSModulePath", "C:/PowerShell")
	t.Setenv("SHELL", "/bin/fish")
	if got := detectShell(); got != "powershell" {
		t.Fatalf("PowerShell detection = %q", got)
	}
	t.Setenv("PSModulePath", "")
	if got := detectShell(); got != "fish" {
		t.Fatalf("fish detection = %q", got)
	}
	t.Setenv("SHELL", "/bin/unsupported")
	if got := detectShell(); got != "bash" {
		t.Fatalf("fallback shell = %q", got)
	}
}

func TestShellCompletionsAndInitCoverSupportedShells(t *testing.T) {
	for _, shell := range supportedShells {
		t.Run(shell, func(t *testing.T) {
			completion := completionScript(shell, "citylist-cli", "en")
			if completion == "" || !strings.Contains(completion, "citylist-cli") || !strings.Contains(completion, "city") {
				t.Fatalf("completion script for %s = %q", shell, completion)
			}
			if init := initCommand(shell, "citylist-cli"); init == "" || !strings.Contains(init, "citylist-cli --shell completions") {
				t.Fatalf("init command for %s = %q", shell, init)
			}
		})
	}
	var help bytes.Buffer
	printShellHelp(&help, "citylist-cli", "en")
	if !strings.Contains(help.String(), "citylist-cli") {
		t.Fatalf("shell help = %q", help.String())
	}
}

func TestRunShellActionRejectsInvalidRequests(t *testing.T) {
	for _, test := range []struct {
		name   string
		action string
		args   []string
	}{
		{name: "too many arguments", action: "init", args: []string{"bash", "fish"}},
		{name: "unsupported shell", action: "init", args: []string{"unknown"}},
		{name: "unsupported action", action: "invalid", args: []string{"bash"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output, errors bytes.Buffer
			if status := runShellAction(&output, &errors, "citylist-cli", test.action, test.args, "en"); status != 2 || errors.Len() == 0 {
				t.Fatalf("runShellAction() = (%d, %q), want error status and message", status, errors.String())
			}
		})
	}
	var output, errors bytes.Buffer
	if status := runShellAction(&output, &errors, "citylist-cli", "completions", []string{"bash"}, "en"); status != 0 || output.Len() == 0 {
		t.Fatalf("valid completion = (%d, %q, %q)", status, output.String(), errors.String())
	}
}
