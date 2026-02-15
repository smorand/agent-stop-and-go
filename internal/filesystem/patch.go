package filesystem

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Hunk represents a single hunk in a unified diff.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []DiffLine
}

// DiffLine represents a single line in a diff hunk.
type DiffLine struct {
	Op   byte   // ' ' context, '+' add, '-' delete
	Text string // Line content (without the op prefix)
}

var hunkHeaderRe = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)

// ParsePatch parses a unified diff into hunks.
func ParsePatch(patch string) ([]Hunk, error) {
	lines := strings.Split(patch, "\n")
	var hunks []Hunk
	var current *Hunk

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Skip --- and +++ header lines
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			continue
		}

		// Parse hunk header
		if matches := hunkHeaderRe.FindStringSubmatch(line); matches != nil {
			if current != nil {
				hunks = append(hunks, *current)
			}
			h := Hunk{}
			h.OldStart, _ = strconv.Atoi(matches[1])
			if matches[2] != "" {
				h.OldCount, _ = strconv.Atoi(matches[2])
			} else {
				h.OldCount = 1
			}
			h.NewStart, _ = strconv.Atoi(matches[3])
			if matches[4] != "" {
				h.NewCount, _ = strconv.Atoi(matches[4])
			} else {
				h.NewCount = 1
			}
			current = &h
			continue
		}

		if current == nil {
			continue
		}

		// Parse diff lines
		if len(line) == 0 {
			// Empty line in diff treated as context with empty content
			current.Lines = append(current.Lines, DiffLine{Op: ' ', Text: ""})
			continue
		}

		switch line[0] {
		case ' ':
			current.Lines = append(current.Lines, DiffLine{Op: ' ', Text: line[1:]})
		case '+':
			current.Lines = append(current.Lines, DiffLine{Op: '+', Text: line[1:]})
		case '-':
			current.Lines = append(current.Lines, DiffLine{Op: '-', Text: line[1:]})
		case '\\':
			// "\ No newline at end of file" — skip
		default:
			// Treat as context line (some diffs omit the space prefix)
			current.Lines = append(current.Lines, DiffLine{Op: ' ', Text: line})
		}
	}

	if current != nil {
		hunks = append(hunks, *current)
	}

	if len(hunks) == 0 {
		return nil, fmt.Errorf("no hunks found in patch")
	}

	return hunks, nil
}

// ApplyPatch applies parsed hunks to the original content.
// Returns the patched content. If any hunk fails, returns an error and original is unchanged.
func ApplyPatch(original string, hunks []Hunk) (string, error) {
	// Split original into lines
	var origLines []string
	if original != "" {
		origLines = strings.Split(original, "\n")
		// If original ends with newline, the split produces an extra empty element
		if len(origLines) > 0 && origLines[len(origLines)-1] == "" && strings.HasSuffix(original, "\n") {
			origLines = origLines[:len(origLines)-1]
		}
	}

	// Apply hunks in order. Track offset as lines are added/removed.
	offset := 0
	for i, hunk := range hunks {
		// Hunk line numbers are 1-based
		startIdx := hunk.OldStart - 1 + offset
		if hunk.OldStart == 0 {
			startIdx = 0 + offset
		}

		// Verify context and deletions match
		checkIdx := startIdx
		for _, dl := range hunk.Lines {
			switch dl.Op {
			case ' ':
				if checkIdx >= len(origLines) || origLines[checkIdx] != dl.Text {
					expected := "<EOF>"
					if checkIdx < len(origLines) {
						expected = origLines[checkIdx]
					}
					return "", fmt.Errorf("patch failed: hunk %d does not match at line %d (expected %q, got %q)",
						i+1, checkIdx+1, dl.Text, expected)
				}
				checkIdx++
			case '-':
				if checkIdx >= len(origLines) || origLines[checkIdx] != dl.Text {
					expected := "<EOF>"
					if checkIdx < len(origLines) {
						expected = origLines[checkIdx]
					}
					return "", fmt.Errorf("patch failed: hunk %d does not match at line %d (expected %q, got %q)",
						i+1, checkIdx+1, dl.Text, expected)
				}
				checkIdx++
			}
		}

		// Apply the hunk
		var newLines []string
		applyIdx := startIdx
		for _, dl := range hunk.Lines {
			switch dl.Op {
			case ' ':
				newLines = append(newLines, origLines[applyIdx])
				applyIdx++
			case '-':
				applyIdx++
			case '+':
				newLines = append(newLines, dl.Text)
			}
		}

		// Replace the affected range in origLines
		oldLen := checkIdx - startIdx
		result := make([]string, 0, len(origLines)-oldLen+len(newLines))
		result = append(result, origLines[:startIdx]...)
		result = append(result, newLines...)
		result = append(result, origLines[startIdx+oldLen:]...)

		offset += len(newLines) - oldLen
		origLines = result
	}

	if len(origLines) == 0 {
		return "", nil
	}
	return strings.Join(origLines, "\n") + "\n", nil
}
