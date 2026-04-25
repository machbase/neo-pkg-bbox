package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDataDir 은 OS별 기본 데이터 디렉터리 경로를 돌려준다.
//   - Windows : %PROGRAMDATA%\neo-blackbox\data (없으면 %APPDATA% fallback)
//   - 그 외   : /data
func DefaultDataDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(windowsAppRoot(), "data")
	}
	return "/data"
}

// DefaultLogDir 은 OS별 기본 로그 디렉터리 경로를 돌려준다.
//   - Windows : %PROGRAMDATA%\neo-blackbox\log (없으면 %APPDATA% fallback)
//   - 그 외   : /var/log/blackbox
func DefaultLogDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(windowsAppRoot(), "log")
	}
	return "/var/log/blackbox"
}

func windowsAppRoot() string {
	if v := os.Getenv("PROGRAMDATA"); v != "" {
		return filepath.Join(v, "neo-blackbox")
	}
	if v := os.Getenv("APPDATA"); v != "" {
		return filepath.Join(v, "neo-blackbox")
	}
	return filepath.Join("C:\\", "ProgramData", "neo-blackbox")
}
