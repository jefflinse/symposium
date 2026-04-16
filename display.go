package main

import (
	"fmt"
	"io"
	"strings"
)

// Display handles terminal output formatting.
type Display struct {
	Out io.Writer
}

func (d *Display) PrintHeader(name string) {
	padding := 60 - len(name) - 4
	if padding < 4 {
		padding = 4
	}
	line := strings.Repeat("─", padding)
	fmt.Fprintf(d.Out, "\n\033[1;36m── %s %s\033[0m\n", name, line)
}

func (d *Display) PrintNewline() {
	fmt.Fprintln(d.Out)
}

func (d *Display) PrintStatus(msg string) {
	fmt.Fprintf(d.Out, "\n\033[33m[%s]\033[0m\n", msg)
}

func (d *Display) PrintError(msg string) {
	// Leading newline: streaming output doesn't end with one, so an error
	// after a partial stream would otherwise tack onto the last content line.
	fmt.Fprintf(d.Out, "\n\033[31m[error: %s]\033[0m\n", msg)
}

// Write implements io.Writer so Display can be passed to LLMClient.Complete
// for streaming output.
func (d *Display) Write(p []byte) (n int, err error) {
	return d.Out.Write(p)
}
