package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

func writeResponseWithPager(output, errorOutput io.Writer, body []byte, format, pager string) error {
	var rendered bytes.Buffer
	if err := writeResponse(&rendered, body, format); err != nil {
		return err
	}
	value := rendered.String()
	file, isFile := output.(*os.File)
	if pager == "never" || !isFile || !isTerminalOutput(file) {
		_, err := io.WriteString(output, value)
		return err
	}
	if pager == "auto" {
		_, height, err := term.GetSize(int(file.Fd()))
		if err == nil && lineCount(value) < height {
			_, writeErr := io.WriteString(output, value)
			return writeErr
		}
	}
	commandPath, err := exec.LookPath("less")
	args := []string{"-R"}
	if err != nil {
		commandPath, err = exec.LookPath("more")
		args = nil
	}
	if err != nil {
		_, writeErr := io.WriteString(output, value)
		return writeErr
	}
	command := exec.Command(commandPath, args...)
	command.Stdin = strings.NewReader(value)
	command.Stdout = output
	command.Stderr = errorOutput
	return command.Run()
}

func isTerminalOutput(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func lineCount(value string) int {
	return bytes.Count([]byte(value), []byte{'\n'})
}
