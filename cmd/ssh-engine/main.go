package main

import (
	"computation-cluster-init/pkg/ssh-engine"
	"k8s.io/klog/v2"
)

func main() {
	err := engine.Control("")
	if err != nil {
		klog.Error(err)
		return
	}
}
