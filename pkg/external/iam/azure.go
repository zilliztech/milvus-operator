package iam

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/pkg/errors"
)

type VerifyAzureParams struct {
	StorageAccount string
	ContainerName  string
}

func newAzureVerificationCredential() (azcore.TokenCredential, error) {
	if os.Getenv("AZURE_CLIENT_SECRET") != "" {
		for _, v := range []string{"AZURE_CLIENT_ID", "AZURE_TENANT_ID"} {
			if os.Getenv(v) == "" {
				return nil, fmt.Errorf("%s must be set when AZURE_CLIENT_SECRET is set", v)
			}
		}
		return azidentity.NewDefaultAzureCredential(nil)
	}
	return azidentity.NewWorkloadIdentityCredential(nil)
}

func VerifyAzure(ctx context.Context, params VerifyAzureParams) error {
	cred, err := newAzureVerificationCredential()
	if err != nil {
		return errors.Wrap(err, "init azure workload identity credential failed")
	}
	client, err := service.NewClient("https://"+params.StorageAccount+".blob.core.windows.net/", cred, &service.ClientOptions{})
	if err != nil {
		return errors.Wrap(err, "init azure storage client failed")
	}
	_, err = client.NewContainerClient(params.ContainerName).GetProperties(ctx, &container.GetPropertiesOptions{})
	return errors.Wrapf(err, "access azure container[%s] properties failed", params.ContainerName)
}
