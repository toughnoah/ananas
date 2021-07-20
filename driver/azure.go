package driver

import (
	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
)

type Azure struct {
	AAD,
	Resource,
	ClientId,
	ClientSecret,
	ClientTenant,
	SubscriptionId,
	Location,
	ResourceGroup string
}

func (az *Azure) NewDiskClient() compute.DisksClient {
	return compute.NewDisksClientWithBaseURI(az.Resource, az.SubscriptionId)
}

func (az *Azure) NewVmClient() compute.VirtualMachinesClient {
	return compute.NewVirtualMachinesClientWithBaseURI(az.Resource, az.SubscriptionId)
}

func (az *Azure) NewAzureAuthorizer() (autorest.Authorizer, error) {
	ccc := auth.NewClientCredentialsConfig(az.ClientId, az.ClientSecret, az.ClientTenant)
	ccc.AADEndpoint = az.AAD
	ccc.Resource = az.Resource
	return ccc.Authorizer()
}

func (az *Azure) NewAzureDisk(size int64, Name string) compute.Disk {
	newDisk := compute.Disk{
		Name:     &Name,
		Location: to.StringPtr(az.Location),
		DiskProperties: &compute.DiskProperties{
			CreationData: &compute.CreationData{
				CreateOption: compute.Empty,
			},
			DiskSizeGB: to.Int32Ptr(64),
		},
		Sku: &compute.DiskSku{
			Name: compute.PremiumLRS,
		},
	}
	return newDisk
}
