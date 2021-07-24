package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/toughnoah/ananas/driver"
	"log"
	"os"
	"os/signal"
	"syscall"
)

const (
	defaultPluginPath  = "/var/lib/kubelet/plugins"
	defaultStagingPath = defaultPluginPath + "/kubernetes.io/csi/pv/"
)


func main() {
	var (
		endpoint       = flag.String("endpoint", "unix:///var/lib/kubelet/plugins/ananas.noah.csi.com/csi.sock", "CSI endpoint.")
		aad            = flag.String("aad", "login.partner.microsoftonline.cn", "Azure AAD Endpoint. default login.partner.microsoftonline.cn")
		azureResource  = flag.String("azure_resource", "management.chinacloudapi.cn", "azure resource management endpoint. default china management.chinacloudapi.cn.")
		clientId       = flag.String("client_id", "", "azure client id.")
		clientSecret   = flag.String("client_secret", "", "azure client secret.")
		clientTenant   = flag.String("client_tenant", "", "azure client tenant.")
		subscriptionId = flag.String("subscription_id", "", "azure subscription id.")
		resourceGroup  = flag.String("resource_group", "", "azure resource group.")
		location       = flag.String("location", "chinaeast2", "azure resource location.")
		// use k8s downward API to get spec.nodename
		nodeId       = flag.String("node", "", "azure k8s node name")
	)
	flag.Parse()
	az := &driver.Azure{
		AAD:            fmt.Sprintf("https://%s", *aad),
		Resource:       fmt.Sprintf("https://%s", *azureResource),
		ClientId:       *clientId,
		ClientSecret:   *clientSecret,
		ClientTenant:   *clientTenant,
		SubscriptionId: *subscriptionId,
		ResourceGroup:  *resourceGroup,
		Location:       *location,
		NodeId:       *nodeId,
	}
	drv, err := driver.NewDriver(*endpoint, az)
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
