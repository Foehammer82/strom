package nodeapi

import (
	"context"
	"log"
	"time"

	internalapi "github.com/Foehammer82/strom/agent/internal/api"
	"github.com/Foehammer82/strom/agent/internal/updates"
)

type AdoptRequest = internalapi.AdoptRequest

type AdoptResponse = internalapi.AdoptResponse

type Service = internalapi.Service

// Checker is the standalone update checker/installer, re-exported so
// callers outside internal/api can construct and inject one.
type Checker = updates.Checker

// Store is the durable release-slot store, re-exported so callers outside
// internal/api can construct and inject one.
type Store = updates.Store

var ErrNodeAlreadyAdopted = internalapi.ErrNodeAlreadyAdopted

type Runner interface {
	CombinedOutput(context.Context, string, ...string) ([]byte, error)
}

type Adopter interface {
	ApplyAdoption(context.Context, AdoptRequest) (AdoptResponse, error)
}

type Options struct {
	Version        string
	Serial         string
	StartedAt      time.Time
	Runner         Runner
	UPSCPath       string
	UPSCmdPath     string
	UPSRWPath      string
	CPUTempPath    string
	RootPath       string
	AdoptionPath   string
	DisableAuth    bool
	AuthPath       string
	UpdatesRoot    string
	UpdatesChecker *updates.Checker
	NUTUser        string
	NUTPassword    string
	Adopter        Adopter
}

func New(logger *log.Logger, opts Options) *Service {
	return internalapi.New(logger, internalapi.Options{
		Version:        opts.Version,
		Serial:         opts.Serial,
		StartedAt:      opts.StartedAt,
		Runner:         opts.Runner,
		UPSCPath:       opts.UPSCPath,
		UPSCmdPath:     opts.UPSCmdPath,
		UPSRWPath:      opts.UPSRWPath,
		CPUTempPath:    opts.CPUTempPath,
		RootPath:       opts.RootPath,
		AdoptionPath:   opts.AdoptionPath,
		DisableAuth:    opts.DisableAuth,
		AuthPath:       opts.AuthPath,
		UpdatesRoot:    opts.UpdatesRoot,
		UpdatesChecker: opts.UpdatesChecker,
		NUTUser:        opts.NUTUser,
		NUTPassword:    opts.NUTPassword,
		Adopter:        opts.Adopter,
	})
}
