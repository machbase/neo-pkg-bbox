package logger

import (
	"io"
	"os"
	"path/filepath"

	"github.com/machbase/neo-pkg-bbox/internal/config"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

var Log *logrus.Logger
var HTTPLog *logrus.Logger

// Init initializes the global logger with logrus and lumberjack
func Init(cfg config.LogConfig) error {
	Log = logrus.New()

	// Set log level
	level, err := logrus.ParseLevel(cfg.Level)
	if err != nil {
		level = logrus.InfoLevel
	}
	Log.SetLevel(level)

	// Set log format
	if cfg.Format == "json" {
		Log.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02 15:04:05",
		})
	} else {
		Log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	}

	// Build full log file path: dir + filename
	logFilePath := cfg.File.Filename
	if cfg.Dir != "" {
		logFilePath = filepath.Join(cfg.Dir, cfg.File.Filename)
	}

	// Set output
	var writers []io.Writer

	switch cfg.Output {
	case "stdout":
		writers = append(writers, os.Stdout)
	case "file":
		// Create log directory if not exists
		logDir := filepath.Dir(logFilePath)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return err
		}

		// Setup lumberjack for log rotation
		fileWriter := &lumberjack.Logger{
			Filename:   logFilePath,
			MaxSize:    cfg.File.MaxSize,
			MaxBackups: cfg.File.MaxBackups,
			MaxAge:     cfg.File.MaxAge,
			Compress:   cfg.File.Compress,
		}
		writers = append(writers, fileWriter)
	case "both":
		writers = append(writers, os.Stdout)

		// Create log directory if not exists
		logDir := filepath.Dir(logFilePath)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return err
		}

		fileWriter := &lumberjack.Logger{
			Filename:   logFilePath,
			MaxSize:    cfg.File.MaxSize,
			MaxBackups: cfg.File.MaxBackups,
			MaxAge:     cfg.File.MaxAge,
			Compress:   cfg.File.Compress,
		}
		writers = append(writers, fileWriter)
	default:
		writers = append(writers, os.Stdout)
	}

	// Set multi writer
	if len(writers) > 1 {
		Log.SetOutput(io.MultiWriter(writers...))
	} else if len(writers) == 1 {
		Log.SetOutput(writers[0])
	}

	Log.Info("Logger initialized successfully")
	return nil
}

// InitHTTPLogger initializes a separate logger for HTTP requests
func InitHTTPLogger(logDir string) error {
	HTTPLog = logrus.New()
	HTTPLog.SetLevel(logrus.InfoLevel)

	// Use text format for HTTP logs
	HTTPLog.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
		DisableColors:   true,
	})

	// Create log directory if not exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	// Setup lumberjack for HTTP log rotation
	httpLogPath := filepath.Join(logDir, "blackbox-http.log")
	fileWriter := &lumberjack.Logger{
		Filename:   httpLogPath,
		MaxSize:    100, // MB
		MaxBackups: 5,
		MaxAge:     30, // days
		Compress:   true,
	}

	HTTPLog.SetOutput(fileWriter)
	HTTPLog.Info("HTTP Logger initialized")
	return nil
}

// GetHTTPLogger returns the HTTP logger instance
func GetHTTPLogger() *logrus.Logger {
	if HTTPLog == nil {
		// Fallback to default logger if not initialized
		HTTPLog = logrus.New()
		HTTPLog.SetLevel(logrus.InfoLevel)
		HTTPLog.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	}
	return HTTPLog
}

// GetLogger returns the global logger instance
func GetLogger() *logrus.Logger {
	if Log == nil {
		// Fallback to default logger if not initialized
		Log = logrus.New()
		Log.SetLevel(logrus.InfoLevel)
		Log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	}
	return Log
}
