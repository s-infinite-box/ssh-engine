module computation-cluster-init

go 1.20

require (
	golang.org/x/crypto v0.13.0
	k8s.io/apimachinery v0.28.2
	k8s.io/klog/v2 v2.100.1
)

require (
	github.com/go-logr/logr v1.2.4 // indirect
	golang.org/x/sys v0.12.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/utils v0.0.0-20230726121419-3b25d923346b // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)

//	go build ./cmd/ssh-engine/main.go