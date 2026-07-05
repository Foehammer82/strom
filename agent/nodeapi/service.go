package nodeapi

import (
	"context"
	"log"
	"time"

	internalapi "github.com/Foehammer82/wattkeeper/agent/internal/api"
)

type AdoptRequest = internalapi.AdoptRequest

type AdoptResponse = internalapi.AdoptResponse

type Service = internalapi.Service

var ErrNodeAlreadyAdopted = internalapi.ErrNodeAlreadyAdopted

type Runner interface {
	CombinedOutput(context.Context, string, ...string) ([]byte, error)
}

type Adopter interface {
	ApplyAdoption(context.Context, AdoptRequest) (AdoptResponse, error)
}

type Options struct {
	Version      string
	Serial       string
	StartedAt    time.Time
	Runner       Runner
	UPSCPath     string
	UPSCmdPath   string
	UPSRWPath    string
	CPUTempPath  string
	RootPath     string
	AdoptionPath string
	DisableAuth  bool
	AuthPath     string
	AgentBinary  string
	NUTUser      string
	NUTPassword  string
	Adopter      Adopter
}

func New(logger *log.Logger, opts Options) *Service {
	return internalapi.New(logger, internalapi.Options{
		Version:      opts.Version,
		Serial:       opts.Serial,
		StartedAt:    opts.StartedAt,
		Runner:       opts.Runner,
		UPSCPath:     opts.UPSCPath,
		UPSCmdPath:   opts.UPSCmdPath,
		UPSRWPath:    opts.UPSRWPath,
		CPUTempPath:  opts.CPUTempPath,
		RootPath:     opts.RootPath,
		AdoptionPath: opts.AdoptionPath,
		DisableAuth:  opts.DisableAuth,
		AuthPath:     opts.AuthPath,
		AgentBinary:  opts.AgentBinary,
		NUTUser:      opts.NUTUser,
		NUTPassword:  opts.NUTPassword,
		Adopter:      opts.Adopter,
	})
}
