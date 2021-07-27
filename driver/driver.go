package driver

import (
	"context"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/sirupsen/logrus"
	. "github.com/toughnoah/ananas/pkg"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
)

const (
	DefaultDriverName = "ananas.noah.csi.com"
)

//  Driver implements the following CSI interfaces:
//
//   csi.IdentityServer
//   csi.ControllerServer
//   csi.NodeServer
//

type Driver struct {
	name        string
	az          *azure.Cloud
	endpoint    string
	srv         *grpc.Server
	nodeId      string
	log         *logrus.Entry
	mounter     Mounter
	volumeLocks *VolumeLocks
}

// NewDriver returns a CSI plugin that contains the necessary gRPC
// interfaces to interact with Kubernetes over unix domain sockets for
// managing DigitalOcean Block Storage
func NewDriver(ep, nodeId string, az *azure.Cloud, mounter Mounter) (*Driver, error) {
	log := logrus.New().WithFields(logrus.Fields{
		"nodeId": nodeId,
	})
	return &Driver{
		az:          az,
		name:        DefaultDriverName,
		endpoint:    ep,
		log:         log,
		nodeId:      nodeId,
		mounter:     mounter,
		volumeLocks: NewVolumeLocks(),
	}, nil
}

func (d *Driver) Run(ctx context.Context) error {
	var eg errgroup.Group
	// log response errors for better observability
	errHandler := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// the actual rpc call
		resp, err := handler(ctx, req)
		if err != nil {
			// posting action
			d.log.WithError(err).WithField("method", info.FullMethod).Error("method failed")
		}
		return resp, err
	}

	d.srv = grpc.NewServer(grpc.UnaryInterceptor(errHandler))
	csi.RegisterIdentityServer(d.srv, d)
	csi.RegisterControllerServer(d.srv, d)
	csi.RegisterNodeServer(d.srv, d)

	u, err := url.Parse(d.endpoint)
	if err != nil {
		return err
	}
	grpcAddr := path.Join(u.Host, filepath.FromSlash(u.Path))
	if u.Host == "" {
		grpcAddr = filepath.FromSlash(u.Path)
	}
	if err := os.Remove(grpcAddr); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove unix domain socket file %s, error: %s", grpcAddr, err)
	}

	grpcListener, err := net.Listen(u.Scheme, grpcAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	eg.Go(func() error {
		go func() {
			<-ctx.Done()
			d.log.Info("server stopped")
			d.srv.GracefulStop()
		}()
		return d.srv.Serve(grpcListener)
	})
	return eg.Wait()
}

func (d *Driver) GetCloud() *azure.Cloud {
	return d.az
}

func (d *Driver) SetLog(log *logrus.Entry) {
	d.log = log
}
