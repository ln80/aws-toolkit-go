package testlog

import (
	"log"
	"testing"
)

const (
	blue   = "\033[34m"
	yellow = "\033[33m"
	reset  = "\033[0m"
	red    = "\033[31m"
)

func doLog(t *testing.T, format string, color string, args ...any) {
	if t != nil {
		t.Helper()
	}
	args = append([]any{color}, args...)
	args = append(args, reset)
	format = "%s" + format + "%s"
	if t != nil {
		t.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func Info(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
	}
	doLog(t, format, blue, args...)
}

func Warn(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
	}
	doLog(t, format, yellow, args...)
}

func Fail(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
	}
	doLog(t, format, red, args...)
}

func Fatal(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
	}
	doLog(t, format, red, args...)
	if t != nil {
		t.Fatal()
	}
	log.Fatal()
}
