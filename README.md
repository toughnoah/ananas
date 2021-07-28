![Go 1.16](https://img.shields.io/badge/Go-v1.16-blue)
[![Go Report Card](https://goreportcard.com/badge/github.com/toughnoah/ananas)](https://goreportcard.com/report/github.com/toughnoah/ananas)
# ananas
Ananas is an experimental project for kubernetes CSI (Container Storage Interface) by using azure disk. Likewise, Ananas is the name of my cute british shorthair.

## References
[csi-digitalocean](https://github.com/digitalocean/csi-digitalocean)

[azuredisk-csi-driver](https://github.com/kubernetes-sigs/azuredisk-csi-driver)

[ceph-csi](https://github.com/ceph/ceph-csi)

[kubernetes](https://github.com/kubernetes/kubernetes)

[container-storage-interface](https://github.com/container-storage-interface/spec)

## To Have Fun
Firstly, please make sure Azure cloud_config is under the path `/etc/kubernetes`

then
```
kubectl create ns ananas
kubectl create -f https://raw.githubusercontent.com/toughnoah/ananas/main/deploy/daemonset.yaml
kubectl create -f https://raw.githubusercontent.com/toughnoah/ananas/main/deploy/pvc.yaml
kubectl create -f https://raw.githubusercontent.com/toughnoah/ananas/main/deploy/sc.yaml
kubectl create -f https://raw.githubusercontent.com/toughnoah/ananas/main/deploy/statefulset.yaml
kubectl create -f https://github.com/toughnoah/ananas/blob/main/deploy/test-pod.yaml
```