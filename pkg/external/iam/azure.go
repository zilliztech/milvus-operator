package iam

import (
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/pkg/errors"
)

type VerifyAzureParams struct {
	StorageAccount string
	ContainerName  string
}

func VerifyAzure(ctx context.Context, params VerifyAzureParams) error {
	cred, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
		ClientID:      os.Getenv("AZURE_CLIENT_ID"),
		TenantID:      os.Getenv("AZURE_TENANT_ID"),
		TokenFilePath: os.Getenv("AZURE_FEDERATED_TOKEN_FILE"),
	})
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
