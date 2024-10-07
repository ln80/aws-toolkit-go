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

func Info(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
	}
	args = append([]any{blue}, args...)
	args = append(args, reset)
	format = "%s" + format + "%s"
	if t != nil {
		t.Logf(format, args...)
		return
	}
	log.Printf("--> "+format, args...)
}

func Warn(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
	}
	args = append([]any{yellow}, args...)
	args = append(args, reset)
	format = "%s" + format + "%s"
	if t != nil {
		t.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func Fail(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
	}
	args = append([]any{red}, args...)
	args = append(args, reset)
	format = "%s" + format + "%s"

	if t != nil {
		t.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func Fatal(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
	}
	args = append([]any{red}, args...)
	args = append(args, reset)
	format = "%s" + format + "%s"

	if t != nil {
		t.Fatalf(format, args...)
	}
	log.Fatalf(format, args...)
}
