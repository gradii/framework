package docker

import "time"

type Image struct {
	Cmd          []string
	Env          []string
	ExposedPorts []string
	Repository   string
	Tag          string
	Args         []string
	// ShmSize sets the size of the container's /dev/shm, for example "1g".
	// Postgres runs parallel workers whose shared memory segments live there,
	// and Docker's 64 MB default is exhausted by a couple of concurrent
	// parallel queries, which then fail with
	// `could not resize shared memory segment ... No space left on device`.
	// The space is tmpfs and is allocated on use, so an image that never
	// touches /dev/shm pays nothing for a larger limit.
	ShmSize string
}

type ImageDriver interface {
	// Build the image.
	Build() error
	// Config gets the image configuration.
	Config() ImageConfig
	// Ready checks if the image is ready by the given function until the given duration, default is 1 minute.
	Ready(func() error, ...time.Duration) error
	// Shutdown the image.
	Shutdown() error
}

type ImageConfig struct {
	ContainerID  string
	ExposedPorts []string
}
