package detect

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// defaultClockFormats are tried in order for Kind="clock_time" when
// Extractor.Format is empty. They cover the 24h form ("15:04") and the 12h
// forms with and without minutes ("3:04pm", "3pm") that real providers emit
// (e.g. Claude Code's "resets 10:20pm" / "resets 4am"). A rule that sets an
// explicit Format keeps single-layout parsing
// (docs/detection.md#reset-time-extractor-kinds).
var defaultClockFormats = []string{"15:04", "3:04pm", "3pm"}

// extractReset runs x against the streams selected by x.Source and
// interprets the first capture group per x.Kind, anchored at now. It
// returns nil when the regex doesn't compile, the regex misses, the
// captured value doesn't parse, or the resulting time is not strictly
// after now ("a Reset Time in the past is ignored" per
// docs/detection.md#reset-time-extractor-kinds).
func extractReset(x *Extractor, in Input, now time.Time) *time.Time {
	if x == nil {
		return nil
	}

	re, err := regexp.Compile(x.Regex)
	if err != nil {
		return nil
	}

	m := re.FindSubmatch(sourceBytes(x.Source, in))
	if len(m) < 2 {
		return nil
	}
	raw := string(m[1])

	t, ok := parseReset(x, raw, now)
	if !ok || !t.After(now) {
		return nil
	}
	return &t
}

// sourceBytes returns the haystack selected by an Extractor's Source field.
// "header" is treated the same as "any" for v1: HTTP headers appear inside
// captured stdout/stderr output rather than as a distinct structured
// source, so both scan the combined stream (see
// docs/detection.md#rule-format).
func sourceBytes(source string, in Input) []byte {
	switch source {
	case "stdout":
		return in.Stdout
	case "stderr":
		return in.Stderr
	default: // "any", "header", or unrecognized
		return concatStreams(in)
	}
}

// parseReset interprets raw per x.Kind. The bool is false when raw does not
// parse under that Kind; the returned time is meaningless in that case.
func parseReset(x *Extractor, raw string, now time.Time) (time.Time, bool) {
	switch x.Kind {
	case "rfc3339":
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, false
		}
		return t, true

	case "unix_seconds":
		sec, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(sec, 0).UTC(), true

	case "http_retry_after":
		if sec, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return now.Add(time.Duration(sec) * time.Second), true
		}
		t, err := http.ParseTime(raw)
		if err != nil {
			return time.Time{}, false
		}
		return t, true

	case "relative_duration":
		// Tolerate a leading "+" ("+2s"); time.ParseDuration rejects it.
		d, err := time.ParseDuration(strings.TrimPrefix(raw, "+"))
		if err != nil {
			return time.Time{}, false
		}
		return now.Add(d), true

	case "clock_time":
		if x.Format != "" {
			parsed, err := time.Parse(x.Format, raw)
			if err != nil {
				return time.Time{}, false
			}
			return nextClockOccurrence(parsed, now), true
		}
		for _, format := range defaultClockFormats {
			if parsed, err := time.Parse(format, raw); err == nil {
				return nextClockOccurrence(parsed, now), true
			}
		}
		return time.Time{}, false

	default:
		return time.Time{}, false
	}
}

// nextClockOccurrence returns the next occurrence of parsed's wall-clock
// time (hour/minute/second/nanosecond) strictly after now, in now's
// location. If today's occurrence is still in the future it is used;
// otherwise it rolls to tomorrow.
func nextClockOccurrence(parsed, now time.Time) time.Time {
	loc := now.Location()
	candidate := time.Date(now.Year(), now.Month(), now.Day(),
		parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}
