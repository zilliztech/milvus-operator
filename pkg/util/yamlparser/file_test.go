package yamlparser

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func GetGitRepoRootDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return strings.TrimSuffix(filename, "pkg/util/yamlparser/file_test.go")
}

func TestParseUserYaml(t *testing.T) {
	t.Run("file not exist", func(t *testing.T) {
		UserYamlPath = GetGitRepoRootDir() + "/test/not_exist.yaml"
		_, err := ParseUserYaml()
		assert.Error(t, err)
	})
	t.Run("ok", func(t *testing.T) {
		UserYamlPath = GetGitRepoRootDir() + "/test/user.yaml"
		ret, err := ParseUserYaml()
		assert.NoError(t, err)
		assert.Equal(t, "myakid", ret.Minio.AccessKeyID)
		assert.Equal(t, "test", ret.Minio.BucketName)
		assert.Equal(t, "gcp", ret.Minio.CloudProvider)
		assert.True(t, ret.Minio.UseIAM)
	})

}
