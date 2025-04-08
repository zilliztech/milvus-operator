package util

import (
	"testing"

	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestGetGitRepoRootDir(t *testing.T) {
	repoRoot := GetGitRepoRootDir()
	assert.NoError(t, config.Init(repoRoot))
}
