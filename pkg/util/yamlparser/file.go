package yamlparser

import (
	"io"
	"os"
	"reflect"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

var UserYamlPath = "/milvus/configs/operator/user.yaml"

type UserYaml struct {
	Minio struct {
		Address       string `yaml:"address"`
		Port          int    `yaml:"port"`
		UseSSL        bool   `yaml:"useSSL"`
		UseIAM        bool   `yaml:"useIAM"`
		CloudProvider string `yaml:"cloudProvider"`
		AccessKeyID   string `yaml:"accessKeyID"`
		BucketName    string `yaml:"bucketName"`
	} `yaml:"minio"`
}

func ParseUserYaml() (*UserYaml, error) {
	userYaml := new(UserYaml)
	err := ParseObjectFromFile(UserYamlPath, userYaml)
	return userYaml, err
}

func ParseObjectFromFile(path string, obj interface{}) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.Wrapf(err, "open file[%s] failed", path)
	}
	defer file.Close()
	fileData, err := io.ReadAll(file)
	if err != nil {
		return errors.Wrapf(err, "read file[%s] failed", path)
	}
	err = yaml.Unmarshal(fileData, obj)
	return errors.Wrapf(err, "unmarshal file[%s] as type[%s] failed", path, reflect.TypeOf(obj).String())
}
