package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/milvus-io/milvus-operator/pkg/external/iam"
	"github.com/milvus-io/milvus-operator/pkg/util/yamlparser"
)

// iam-verify command is expected to run in the initContainer before the milvus container starts
// It verifies if it can assume the given role in the environment, and if the bucket is accessible.
// if not, it loops sleep and retries until the conditions are met.

func main() {
	userYaml, err := yamlparser.ParseUserYaml()
	if err != nil {
		log.Fatalf("parse user.yaml failed: %v\n", err)
	}
	if !userYaml.Minio.UseIAM {
		log.Println("useIAM is false")
		os.Exit(0)
		return
	}
	var verifyFunc func(ctx context.Context) error
	switch userYaml.Minio.CloudProvider {
	case "gcp":
		verifyFunc = func(ctx context.Context) error {
			return iam.VerifyGCP(ctx, userYaml.Minio.BucketName)
		}
	case "azure":
		verifyFunc = func(ctx context.Context) error {
			return iam.VerifyAzure(ctx, iam.VerifyAzureParams{
				StorageAccount: userYaml.Minio.AccessKeyID,
				ContainerName:  userYaml.Minio.BucketName,
			})
		}
	case "aws":
		verifyFunc = func(ctx context.Context) error {
			address := fmt.Sprintf("%s:%d", userYaml.Minio.Address, userYaml.Minio.Port)
			return iam.VerifyAWS(ctx, userYaml.Minio.BucketName, userYaml.Minio.Region, address, userYaml.Minio.UseSSL)
		}
	case "aliyun":
		verifyFunc = func(ctx context.Context) error {
			address := fmt.Sprintf("%s:%d", userYaml.Minio.Address, userYaml.Minio.Port)
			return iam.VerifyAliyun(ctx, userYaml.Minio.BucketName, userYaml.Minio.Region, address, userYaml.Minio.UseSSL)
		}
	case "tencent":
		verifyFunc = func(ctx context.Context) error {
			address := fmt.Sprintf("%s:%d", userYaml.Minio.Address, userYaml.Minio.Port)
			return iam.VerifyTencent(ctx, userYaml.Minio.BucketName, userYaml.Minio.Region, address, userYaml.Minio.UseSSL)
		}
	default:
		log.Printf("iam-verify for csp %s not implement, assume success\n", userYaml.Minio.CloudProvider)
		os.Exit(0)
		return
	}
	for {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()
			err := verifyFunc(ctx)
			if err == nil {
				log.Println("verify iam success")
				os.Exit(0)
				return
			}
			log.Printf("verify iam failed: %v, retry in 5 seconds\n", err)
			time.Sleep(time.Second * 5)
		}()
	}
}
