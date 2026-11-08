// Package diff renders unified diffs.
package diff

import (
	"fmt"
	"slices"
	"strings"
)

const context = 3

// Unified returns a unified diff of old to new for path, with 3 lines of context.
// It returns "" when the texts are equal.
// ponytail: line ends are not compared, so a missing final newline shows no marker; add "\ No newline at end of file" if that matters.
func Unified(path, old, new string) string {
	a, b := splitLines(old), splitLines(new)
	// lcs[i][j] is the longest common subsequence length of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i, v := range slices.Backward(a) {
		for j := len(b) - 1; j >= 0; j-- {
			if v == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		tag          byte // ' ', '-' or '+'
		text         string
		aLine, bLine int // lines of a and b before this op
	}
	var ops []op
	var changed []int // indexes of '-' and '+' ops
	for i, j := 0, 0; i < len(a) || j < len(b); {
		o := op{aLine: i, bLine: j}
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			o.tag, o.text, i, j = ' ', a[i], i+1, j+1
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			o.tag, o.text, i = '-', a[i], i+1
		default:
			o.tag, o.text, j = '+', b[j], j+1
		}
		if o.tag != ' ' {
			changed = append(changed, len(ops))
		}
		ops = append(ops, o)
	}
	if len(changed) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", path, path)
	for k := 0; k < len(changed); {
		first, last := changed[k], changed[k]
		for k++; k < len(changed) && changed[k]-last <= 2*context+1; k++ {
			last = changed[k]
		}
		from, to := max(first-context, 0), min(last+context+1, len(ops))
		aCount, bCount := 0, 0
		for _, o := range ops[from:to] {
			if o.tag != '+' {
				aCount++
			}
			if o.tag != '-' {
				bCount++
			}
		}
		aStart, bStart := ops[from].aLine+1, ops[from].bLine+1
		if aCount == 0 {
			aStart--
		}
		if bCount == 0 {
			bStart--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
		for _, o := range ops[from:to] {
			out.WriteString(string(o.tag))
			out.WriteString(o.text)
			out.WriteString("\n")
		}
	}
	return out.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
