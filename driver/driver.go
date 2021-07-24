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
)

const (
	DefaultDriverName = "azure.noah.csi.com"
)

//  Driver implements the following CSI interfaces:
//
//   csi.IdentityServer
//   csi.ControllerServer
//   csi.NodeServer
//

type Driver struct {
	name        string
	az          Azure
	endpoint    string
	srv         *grpc.Server
	log         *logrus.Entry
	mounter     *mounter
	volumeLocks *VolumeLocks
}

// NewDriver returns a CSI plugin that contains the necessary gRPC
// interfaces to interact with Kubernetes over unix domain sockets for
// managing DigitalOcean Block Storage
func NewDriver(ep string, az *Azure) (*Driver, error) {
	log := logrus.New().WithFields(logrus.Fields{
		"resource_group":  az.ResourceGroup,
		"subscription_id": az.SubscriptionId,
		"Location":        az.Location,
	})
	return &Driver{
		az:          *az,
		name:        DefaultDriverName,
		endpoint:    ep,
		log:         log,
		mounter:     newMounter(log),
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
