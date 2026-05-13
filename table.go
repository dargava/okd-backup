package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Table is a simple table renderer with Unicode box-drawing characters and ANSI color support.
type Table struct {
	title      string
	headers    []string
	headerFmt  []string
	colFmt     []func(string) string
	rows       [][]string
	rightAlign []bool
}

func newTable(title string) *Table {
	return &Table{title: title}
}

func (t *Table) addColumn(header string, opts ...interface{}) {
	t.headers = append(t.headers, header)
	t.headerFmt = append(t.headerFmt, bold(colorBlue+header+colorReset))
	t.colFmt = append(t.colFmt, nil)
	t.rightAlign = append(t.rightAlign, false)

	for _, opt := range opts {
		switch v := opt.(type) {
		case func(string) string:
			t.colFmt[len(t.colFmt)-1] = v
		case string:
			if v == "right" {
				t.rightAlign[len(t.rightAlign)-1] = true
			}
		}
	}
}

func (t *Table) addRow(cells ...string) {
	row := make([]string, len(t.headers))
	for i, c := range cells {
		if i < len(row) {
			row[i] = c
		}
	}
	t.rows = append(t.rows, row)
}

// visLen returns the printable length of a string, ignoring ANSI escape codes.
func visLen(s string) int {
	// Strip ANSI escape sequences
	inEscape := false
	length := 0
	for i := 0; i < len(s); {
		if inEscape {
			if s[i] == 'm' {
				inEscape = false
			}
			i++
			continue
		}
		if s[i] == '\033' {
			inEscape = true
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		length++
		i += size
	}
	return length
}

func padRight(s string, width int) string {
	n := visLen(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func padLeft(s string, width int) string {
	n := visLen(s)
	if n >= width {
		return s
	}
	return strings.Repeat(" ", width-n) + s
}

func (t *Table) print() {
	ncols := len(t.headers)
	if ncols == 0 {
		return
	}

	// Calculate column widths
	widths := make([]int, ncols)
	for i, h := range t.headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i < ncols {
				n := visLen(cell)
				if n > widths[i] {
					widths[i] = n
				}
			}
		}
	}

	// Box-drawing helpers
	hbar := func(left, mid, right string) string {
		s := left
		for i, w := range widths {
			s += strings.Repeat("─", w+2)
			if i < ncols-1 {
				s += mid
			}
		}
		return s + right
	}

	// Print title
	if t.title != "" {
		fmt.Printf("\n%s%s%s\n", colorBold+colorBlue, t.title, colorReset)
	}

	fmt.Println(hbar("┌", "┬", "┐"))

	// Print header
	fmt.Print("│")
	for i, h := range t.headers {
		cell := colorBold + colorBlue + h + colorReset
		fmt.Printf(" %s │", padRight(cell, widths[i]))
	}
	fmt.Println()
	fmt.Println(hbar("├", "┼", "┤"))

	// Print rows
	for _, row := range t.rows {
		fmt.Print("│")
		for i := 0; i < ncols; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			if t.colFmt[i] != nil {
				cell = t.colFmt[i](cell)
			}
			if t.rightAlign[i] {
				fmt.Printf(" %s │", padLeft(cell, widths[i]))
			} else {
				fmt.Printf(" %s │", padRight(cell, widths[i]))
			}
		}
		fmt.Println()
	}

	fmt.Println(hbar("└", "┴", "┘"))
}
