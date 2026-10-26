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

// newAzureVerificationCredential selects the Azure token credential using WI → SPN priority.
// WI is checked first so that pods with valid Workload Identity config are not silently
// switched when AZURE_CLIENT_SECRET is also present in the environment.
func newAzureVerificationCredential() (azcore.TokenCredential, error) {
	if os.Getenv("AZURE_FEDERATED_TOKEN_FILE") != "" {
		return azidentity.NewWorkloadIdentityCredential(nil)
	}
	if secret := os.Getenv("AZURE_CLIENT_SECRET"); secret != "" {
		tenantID := os.Getenv("AZURE_TENANT_ID")
		clientID := os.Getenv("AZURE_CLIENT_ID")
		if tenantID == "" || clientID == "" {
			return nil, fmt.Errorf("SPN authentication requires AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET")
		}
		return azidentity.NewClientSecretCredential(tenantID, clientID, secret, nil)
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
