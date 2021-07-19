package driver

import (
	"fmt"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure/auth"
)

type Azure struct {
	AAD,
	Resource,
	ClientId,
	ClientSecret,
	ClientTenant,
	SubscriptionId,
	ResourceGroup string
}

func (az *Azure) NewDiskClient() compute.DisksClient {
	return compute.NewDisksClientWithBaseURI(fmt.Sprintf("https://%s", az.Resource), az.SubscriptionId)
}

func (az *Azure) NewVmClient() compute.VirtualMachinesClient {
	return compute.NewVirtualMachinesClientWithBaseURI(fmt.Sprintf("https://%s", az.Resource), az.SubscriptionId)
}

func (az *Azure) NewAzureAuthorizer() (autorest.Authorizer, error) {
	ccc := auth.NewClientCredentialsConfig(az.ClientId, az.ClientSecret, az.ClientTenant)
	ccc.AADEndpoint = fmt.Sprintf("https://%s", az.AAD)
	ccc.Resource = fmt.Sprintf("https://%s", az.Resource)
	return ccc.Authorizer()
}
