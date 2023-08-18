package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zilliztech/milvus-operator/pkg/config"
)

func TestGetGitRepoRootDir(t *testing.T) {
	repoRoot := GetGitRepoRootDir()
	assert.NoError(t, config.Init(repoRoot))
}
