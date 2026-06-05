package output

import (
	"fmt"
	"os"
)

const (
	ansiReset    = "\033[0m"
	ansiBold     = "\033[1m"
	ansiRed      = "\033[31m"
	ansiGreen    = "\033[32m"
	ansiYellow   = "\033[33m"
	ansiBlue     = "\033[34m"
	ansiCyan     = "\033[36m"
	ansiGray     = "\033[90m"
	ansiBoldCyan = "\033[1;36m"
)

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

var noColor = os.Getenv("NO_COLOR") != "" || !isTerminal(os.Stdout)

// Quiet suppresses auxiliary text output when true.
var Quiet bool

func colorize(code, msg string) string {
	if noColor {
		return msg
	}
	return code + msg + ansiReset
}

func Success(msg string) {
	fmt.Println(colorize(ansiGreen, "✔ "+msg))
}

func Error(msg string) {
	fmt.Fprintln(os.Stderr, colorize(ansiRed, "✖ "+msg))
}

func Warn(msg string) {
	fmt.Fprintln(os.Stderr, colorize(ansiYellow, "⚠ "+msg))
}

func Info(msg string) {
	fmt.Println(colorize(ansiBlue, "ℹ "+msg))
}

func Bold(msg string) {
	fmt.Println(colorize(ansiBold, msg))
}

func Gray(msg string) {
	fmt.Println(colorize(ansiGray, msg))
}

func AuxInfo(msg string) {
	if Quiet {
		return
	}
	Info(msg)
}

func AuxBold(msg string) {
	if Quiet {
		return
	}
	Bold(msg)
}

func AuxGray(msg string) {
	if Quiet {
		return
	}
	Gray(msg)
}

func AuxWarn(msg string) {
	if Quiet {
		return
	}
	Warn(msg)
}

func FormatCyanBold(s string) string { return colorize(ansiBoldCyan, s) }
func FormatGray(s string) string     { return colorize(ansiGray, s) }
