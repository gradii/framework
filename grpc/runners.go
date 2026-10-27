package grpc

import (
	"github.com/gradii/framework/contracts/config"
	"github.com/gradii/framework/contracts/grpc"
)

type GrpcRunner struct {
	config config.Config
	grpc   grpc.Grpc
}

func NewGrpcRunner(config config.Config, grpc grpc.Grpc) *GrpcRunner {
	return &GrpcRunner{
		config: config,
		grpc:   grpc,
	}
}

func (r *GrpcRunner) Signature() string {
	return "goravel:grpc"
}

func (r *GrpcRunner) ShouldRun() bool {
	return r.grpc != nil && r.config.GetString("grpc.host") != ""
}

func (r *GrpcRunner) Run() error {
	return r.grpc.Run()
}

func (r *GrpcRunner) Shutdown() error {
	return r.grpc.Shutdown()
}
