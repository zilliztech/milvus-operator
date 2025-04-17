package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zilliztech/milvus-operator/pkg/util"
)

func TestInit_NewConfigFailed(t *testing.T) {
	workDir := "/a-bad-path/a-bad-path"
	err := Init(workDir)
	assert.Error(t, err)
}

func TestInit_Success(t *testing.T) {
	workDir := util.GetGitRepoRootDir()
	err := Init(workDir)
	assert.NoError(t, err)
	assert.Equal(t, false, defaultConfig.debugMode)
}

func TestInit_Debug(t *testing.T) {
	os.Setenv("DEBUG", "true")
	workDir := util.GetGitRepoRootDir()
	err := Init(workDir)
	assert.NoError(t, err)
	assert.Equal(t, true, defaultConfig.debugMode)
	assert.True(t, IsDebug())
}

func TestConfig_GetTemplate(t *testing.T) {
	defaultConfig = &Config{
		templates: map[string]string{
			"key": "value",
		},
	}

	assert.Equal(t, "value", defaultConfig.GetTemplate("key"))
}
func TestGetMilvusConfigTemplate(t *testing.T) {
	defaultConfig = &Config{
		templates: map[string]string{
			MilvusConfigTpl:        "value",
			MilvusClusterConfigTpl: "value2",
		},
	}
	assert.Equal(t, "value2", GetMilvusClusterConfigTemplate())
	assert.Equal(t, "value", GetMilvusConfigTemplate())
}
