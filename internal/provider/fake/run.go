package fake

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Main interprets one attempt block of a fake-provider script, writing the
// scripted output and returning the scripted exit code. Binaries dispatch
// to it when argv[1] == RunMode (the defib main does; test binaries do the
// same from TestMain). now is injectable so reset-at output is testable.
func Main(args []string, stdout, stderr io.Writer, now func() time.Time) int {
	fs := flag.NewFlagSet(RunMode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	script := fs.String("script", "", "path to the fake provider script")
	block := fs.Int("block", 1, "1-based attempt block to replay")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	data, err := os.ReadFile(*script)
	if err != nil {
		fmt.Fprintf(stderr, "fake provider: %v\n", err)
		return 2
	}
	blocks := splitBlocks(string(data))
	if *block < 1 || *block > len(blocks) {
		fmt.Fprintf(stderr, "fake provider: script has %d attempt block(s), block %d requested\n",
			len(blocks), *block)
		return 2
	}

	code, err := runBlock(blocks[*block-1], stdout, stderr, now)
	if err != nil {
		fmt.Fprintf(stderr, "fake provider: %v\n", err)
		return 2
	}
	return code
}

// splitBlocks separates the script into attempt blocks on blank lines,
// skipping full-line comments.
func splitBlocks(script string) [][]string {
	var blocks [][]string
	var current []string
	flush := func() {
		if len(current) > 0 {
			blocks = append(blocks, current)
			current = nil
		}
	}
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flush()
		case strings.HasPrefix(trimmed, "#"):
			// comment-only line: keeps the current block open
		default:
			current = append(current, trimmed)
		}
	}
	flush()
	return blocks
}

// runBlock executes one attempt block's directives in order and returns the
// attempt's exit code (0 unless an exit directive says otherwise).
func runBlock(lines []string, stdout, stderr io.Writer, now func() time.Time) (int, error) {
	for _, line := range lines {
		directive, rest, err := parseLine(line)
		if err != nil {
			return 0, err
		}
		switch directive {
		case "emit":
			text, err := quoted(line, rest)
			if err != nil {
				return 0, err
			}
			fmt.Fprintln(stdout, text)
		case "emit-err":
			text, err := quoted(line, rest)
			if err != nil {
				return 0, err
			}
			fmt.Fprintln(stderr, text)
		case "sleep":
			d, err := time.ParseDuration(firstToken(rest))
			if err != nil {
				return 0, fmt.Errorf("directive %q: %w", line, err)
			}
			time.Sleep(d)
		case "reset-at":
			arg := firstToken(rest)
			d, err := time.ParseDuration(strings.TrimPrefix(arg, "+"))
			if err != nil {
				return 0, fmt.Errorf("directive %q: %w", line, err)
			}
			fmt.Fprintf(stdout, "FAKE_RESET_AT=%s\n", now().Add(d).UTC().Format(time.RFC3339))
		case "exit":
			code, err := strconv.Atoi(firstToken(rest))
			if err != nil {
				return 0, fmt.Errorf("directive %q: %w", line, err)
			}
			return code, nil
		default:
			return 0, fmt.Errorf("unknown directive %q", line)
		}
	}
	return 0, nil
}

// parseLine strips the mandatory "attempt:" prefix and splits off the
// directive word.
func parseLine(line string) (directive, rest string, err error) {
	after, ok := strings.CutPrefix(line, "attempt:")
	if !ok {
		return "", "", fmt.Errorf("directive %q: missing %q prefix", line, "attempt:")
	}
	after = strings.TrimSpace(after)
	directive, rest, _ = strings.Cut(after, " ")
	if directive == "" {
		return "", "", fmt.Errorf("directive %q: empty", line)
	}
	return directive, strings.TrimSpace(rest), nil
}

// quoted extracts the double-quoted argument of emit/emit-err; anything
// after the closing quote (e.g. a trailing comment) is ignored.
func quoted(line, rest string) (string, error) {
	start := strings.IndexByte(rest, '"')
	if start < 0 {
		return "", fmt.Errorf("directive %q: expected a quoted string", line)
	}
	end := strings.IndexByte(rest[start+1:], '"')
	if end < 0 {
		return "", fmt.Errorf("directive %q: unterminated quote", line)
	}
	return rest[start+1 : start+1+end], nil
}

// firstToken returns rest up to the first space or comment marker.
func firstToken(rest string) string {
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
