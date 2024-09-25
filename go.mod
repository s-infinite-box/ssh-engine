module computation-cluster-init

go 1.21

toolchain go1.22.1

require (
	golang.org/x/crypto v0.23.0
	k8s.io/apimachinery v0.29.2
	k8s.io/klog/v2 v2.120.1
)

require (
	github.com/go-logr/logr v1.4.2 // indirect
	golang.org/x/sys v0.25.0 // indirect
	k8s.io/utils v0.0.0-20240921022957-49e7df575cb6 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

//	go build ./cmd/ssh-engine/main.go
