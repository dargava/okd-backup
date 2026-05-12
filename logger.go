package main

import (
	"fmt"
	"os"
)

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
)

var verboseMode bool

func setVerbose(v bool) { verboseMode = v }

func logSection(title string) {
	fmt.Printf("\n%s%s── %s ──%s\n", colorBold, colorBlue, title, colorReset)
}

func logInfo(msg string) {
	fmt.Println(msg)
}

func logSuccess(msg string) {
	fmt.Printf("%s✔ %s%s\n", colorGreen, msg, colorReset)
}

func logWarning(msg string) {
	fmt.Fprintf(os.Stderr, "%s⚠ %s%s\n", colorYellow, msg, colorReset)
}

func logError(msg string) {
	fmt.Fprintf(os.Stderr, "%s✖ %s%s\n", colorRed, msg, colorReset)
}

func logVerbose(msg string) {
	if verboseMode {
		fmt.Printf("%s%s%s\n", colorDim, msg, colorReset)
	}
}

func logDim(msg string) {
	fmt.Printf("%s%s%s\n", colorDim, msg, colorReset)
}

func bold(s string) string   { return colorBold + s + colorReset }
func cyan(s string) string   { return colorCyan + s + colorReset }
func green(s string) string  { return colorGreen + s + colorReset }
func yellow(s string) string { return colorYellow + s + colorReset }
func dim(s string) string    { return colorDim + s + colorReset }
func red(s string) string    { return colorRed + s + colorReset }
