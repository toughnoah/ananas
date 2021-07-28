package main

import (
	"context"
	"flag"
	"github.com/toughnoah/ananas/driver"
	"log"
	"os"
	"os/signal"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
	"syscall"
)

func main() {
	var (
		endpoint = flag.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint.")
		config   = flag.String("config", "/etc/kubernetes/cloud_config", "cloud config path.")
		// use k8s downward API to get spec.nodename
		nodeId = flag.String("node", "", "azure k8s node name")
	)
	flag.Parse()
	cfg, err := os.Open(*config)
	if err != nil {
		log.Fatalln(err)
	}
	az, err := azure.NewCloudWithoutFeatureGates(cfg, false)
	if err != nil {
		log.Fatalln(err)
	}

	mounter := driver.NewMounter()

	drv, err := driver.NewDriver(*endpoint, *nodeId, az, mounter)
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
