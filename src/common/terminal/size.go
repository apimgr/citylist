package terminal

import (
	"os"
	"strconv"

	"golang.org/x/term"
)

type SizeMode uint8

const (
	SizeModeMicro SizeMode = iota
	SizeModeMinimal
	SizeModeCompact
	SizeModeStandard
	SizeModeWide
	SizeModeUltrawide
	SizeModeMassive
)

type TerminalSize struct {
	Width  int
	Height int
	Mode   SizeMode
}

func GetTerminalSize() TerminalSize {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		width = envSize("COLUMNS", 80)
		height = envSize("LINES", 24)
	}
	return DetectTerminalSize(width, height)
}

func DetectTerminalSize(width, height int) TerminalSize {
	if width < 1 {
		width = 80
	}
	if height < 1 {
		height = 24
	}
	var mode SizeMode
	switch {
	case width >= 400 && height >= 80:
		mode = SizeModeMassive
	case width >= 200 && height >= 60:
		mode = SizeModeUltrawide
	case width >= 120 && height >= 40:
		mode = SizeModeWide
	case width >= 80 && height >= 24:
		mode = SizeModeStandard
	case width >= 60 && height >= 16:
		mode = SizeModeCompact
	case width >= 40 && height >= 10:
		mode = SizeModeMinimal
	default:
		mode = SizeModeMicro
	}
	return TerminalSize{Width: width, Height: height, Mode: mode}
}

func (mode SizeMode) MaxTableColumns() int {
	switch mode {
	case SizeModeMicro:
		return 2
	case SizeModeMinimal:
		return 3
	case SizeModeCompact:
		return 4
	case SizeModeStandard:
		return 6
	default:
		return 10
	}
}

func (mode SizeMode) String() string {
	switch mode {
	case SizeModeMicro:
		return "micro"
	case SizeModeMinimal:
		return "minimal"
	case SizeModeCompact:
		return "compact"
	case SizeModeStandard:
		return "standard"
	case SizeModeWide:
		return "wide"
	case SizeModeUltrawide:
		return "ultrawide"
	case SizeModeMassive:
		return "massive"
	default:
		return "unknown"
	}
}

func envSize(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
