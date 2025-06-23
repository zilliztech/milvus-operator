package util

import (
	"runtime"
	"strings"
)

func GetGitRepoRootDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return strings.TrimSuffix(filename, "pkg/util/testing.go")
}
