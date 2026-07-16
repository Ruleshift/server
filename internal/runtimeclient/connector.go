package runtimeclient

import (
	"context"
	"encoding/hex"
	"slices"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	modulev1 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev1"
	"google.golang.org/grpc"
)

type Connector struct{}

func (Connector) Connect(ctx context.Context, deployment controlplane.RuntimeDeployment, version controlplane.Version) (module.Runtime, controlplane.Description, error) {
	endpoint := Endpoint{
		Address:            deployment.Endpoint,
		Token:              deployment.RPCToken,
		StateTypeURL:       version.Manifest.StateTypeURL,
		CommandTypeURLs:    version.Manifest.CommandTypeURLs,
		TransitionDeadline: time.Duration(version.Manifest.TransitionDeadlineMS) * time.Millisecond,
	}
	connection, err := newConnection(endpoint)
	if err != nil {
		return nil, controlplane.Description{}, err
	}
	runtime, err := runtimeFor(connection, endpoint)
	if err != nil {
		_ = connection.Close()
		return nil, controlplane.Description{}, err
	}
	response, err := modulev1.NewModuleRuntimeClient(connection).Describe(ctx, &modulev1.DescribeRequest{}, grpc.WaitForReady(true))
	if err != nil {
		_ = connection.Close()
		return nil, controlplane.Description{}, err
	}
	return runtime, descriptionFrom(response), nil
}

func descriptionFrom(response *modulev1.DescribeResponse) controlplane.Description {
	return controlplane.Description{
		ModuleID:           response.ModuleId,
		Version:            response.Version,
		ABIVersion:         response.AbiVersion,
		StateTypeURL:       response.StateTypeUrl,
		CommandTypeURLs:    slices.Clone(response.CommandTypeUrls),
		DescriptorDigest:   "sha256:" + hex.EncodeToString(response.DescriptorSetSha256),
		SupportsPlayerLeft: response.SupportsPlayerLeft,
	}
}
