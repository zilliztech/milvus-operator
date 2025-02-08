package iam

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
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
		return fmt.Errorf("init azure workload identity credential failed: %w", err)
	}
	client, err := service.NewClient("https://"+params.StorageAccount+".blob.core.windows.net/", cred, &service.ClientOptions{})
	if err != nil {
		return fmt.Errorf("init azure storage client failed: %w", err)
	}
	_, err = client.NewContainerClient(params.ContainerName).GetProperties(ctx, &container.GetPropertiesOptions{})
	return fmt.Errorf("access azure container[%s] properties failed: %w", params.ContainerName, err)
}
