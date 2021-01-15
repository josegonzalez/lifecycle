module github.com/buildpacks/lifecycle/tools

go 1.15

require (
	github.com/Azure/azure-sdk-for-go v42.3.0+incompatible // indirect
	github.com/Azure/go-autorest/autorest v0.10.2 // indirect
	github.com/Azure/go-autorest/autorest/validation v0.2.0 // indirect
	github.com/BurntSushi/toml v0.3.1
	github.com/aws/aws-sdk-go v1.31.6 // indirect
	github.com/buildpacks/imgutil v0.0.0-20210115182929-e2b7b1a5467a
	github.com/buildpacks/lifecycle v0.9.2
	github.com/docker/docker v1.4.2-0.20190924003213-a8608b5b67c7
	github.com/golang/mock v1.4.4
	github.com/golangci/golangci-lint v1.30.0
	github.com/google/go-containerregistry v0.4.0
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/pkg/errors v0.9.1
	github.com/sclevine/yj v0.0.0-20190506050358-d9a48607cc5c
	github.com/vdemeester/k8s-pkg-credentialprovider v1.18.1-0.20201019120933-f1d16962a4db // indirect
	golang.org/x/tools v0.0.0-20200916195026-c9a70fc28ce3
	gonum.org/v1/netlib v0.0.0-20190331212654-76723241ea4e // indirect
	sigs.k8s.io/structured-merge-diff v0.0.0-20190525122527-15d366b2352e // indirect
)

replace github.com/buildpacks/lifecycle v0.9.2 => ../
