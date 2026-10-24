package logging

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"google.golang.org/grpc/grpclog"
)

// NewGRPCLogger preserves diagnostic severity/verbosity and the source call site,
// but never formats library arguments: transport warnings can contain arbitrary
// unauthenticated metadata before Catalog's auth interceptor runs.
func NewGRPCLogger(output io.Writer) grpclog.LoggerV2 {
	minimum := 2 // Match gRPC's default error severity.
	switch strings.ToLower(os.Getenv("GRPC_GO_LOG_SEVERITY_LEVEL")) {
	case "info":
		minimum = 0
	case "warning":
		minimum = 1
	}
	verbosity, _ := strconv.Atoi(os.Getenv("GRPC_GO_LOG_VERBOSITY_LEVEL"))
	return &grpcLogger{logger: log.New(output, "", log.LstdFlags), minimum: minimum, verbosity: verbosity}
}

type grpcLogger struct {
	logger             *log.Logger
	minimum, verbosity int
}

func (l *grpcLogger) Info(...any)             { l.emit(0) }
func (l *grpcLogger) Infoln(...any)           { l.emit(0) }
func (l *grpcLogger) Infof(string, ...any)    { l.emit(0) }
func (l *grpcLogger) Warning(...any)          { l.emit(1) }
func (l *grpcLogger) Warningln(...any)        { l.emit(1) }
func (l *grpcLogger) Warningf(string, ...any) { l.emit(1) }
func (l *grpcLogger) Error(...any)            { l.emit(2) }
func (l *grpcLogger) Errorln(...any)          { l.emit(2) }
func (l *grpcLogger) Errorf(string, ...any)   { l.emit(2) }
func (l *grpcLogger) Fatal(...any)            { l.emit(3); os.Exit(1) }
func (l *grpcLogger) Fatalln(...any)          { l.emit(3); os.Exit(1) }
func (l *grpcLogger) Fatalf(string, ...any)   { l.emit(3); os.Exit(1) }
func (l *grpcLogger) V(level int) bool        { return level <= l.verbosity }

func (l *grpcLogger) emit(level int) {
	if level < l.minimum {
		return
	}
	var callers [12]uintptr
	frames := runtime.CallersFrames(callers[:runtime.Callers(3, callers[:])])
	source, line := "grpc", 0
	for {
		frame, more := frames.Next()
		if !strings.HasPrefix(frame.Function, "google.golang.org/grpc/grpclog.") &&
			!strings.HasPrefix(frame.Function, "google.golang.org/grpc/internal/grpclog.") {
			source, line = filepath.Base(frame.File), frame.Line
			break
		}
		if !more {
			break
		}
	}
	l.logger.Printf("[gRPC] severity=%s source=%s:%d", [...]string{"info", "warning", "error", "fatal"}[level], source, line)
}
