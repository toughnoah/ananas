package main

import (
	"context"
	"flag"
	"github.com/toughnoah/ananas/driver"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var (
		endpoint   = flag.String("endpoint", "unix:///var/lib/kubelet/plugins/azure.noah.csi.com/csi.sock", "CSI endpoint.")
		aad   = flag.String("aad", "login.partner.microsoftonline.cn", "Azure AAD Endpoint.")
		azureResource   = flag.String("azure_resource", "management.chinacloudapi.cn", "azure resource management endpoint.")
		clientId   = flag.String("client_id", "", "azure client id.")
		clientSecret   = flag.String("client_secret", "", "azure client secret.")
		clientTenant   = flag.String("client_tenant", "", "azure client tenant.")
		subscriptionId   = flag.String("subscription_id", "", "azure subscription id.")
		resourceGroup   = flag.String("resource_group", "", "azure resource group.")
	)
	az :=&driver.Azure{
		AAD: *aad,
		Resource: *azureResource,
		ClientId: *clientId,
		ClientSecret: *clientSecret,
		ClientTenant: *clientTenant,
		SubscriptionId: *subscriptionId,
		ResourceGroup: *resourceGroup,
	}
	drv, err := driver.NewDriver(*endpoint,az)
	if err != nil {
		log.Fatalln(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
	}()

	if err := drv.Run(ctx); err != nil {
		log.Fatalln(err)
	}
}