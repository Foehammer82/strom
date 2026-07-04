package nodeapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/Foehammer82/wattkeeper/agent/internal/nutconf"
)

type Reloader interface {
	Reload(context.Context, bool, []string) error
}

type AdoptedUpdater interface {
	UpdateAdopted(bool)
}

type InventoryCredentialsUpdater interface {
	UpdateNUTCredentials(string, string)
}

type RuntimeAdopter struct {
	Logger       *log.Logger
	ConfigDir    string
	AdoptionPath string
	Reloader     Reloader
	Advertiser   AdoptedUpdater
	Inventory    InventoryCredentialsUpdater
	Version      string
	Serial       string
	TLSPort      int
	TLSCertPath  string
	TLSKeyPath   string
}

type adoptionState struct {
	CAPEM          string    `json:"ca_pem"`
	NUTUser        string    `json:"nut_user"`
	NUTPassword    string    `json:"nut_password"`
	TokenSHA256    string    `json:"token_sha256"`
	ControllerURL  string    `json:"controller_url"`
	TLSPort        int       `json:"tls_port"`
	TLSFingerprint string    `json:"tls_fingerprint"`
	AdoptedAt      time.Time `json:"adopted_at"`
}

func (a *RuntimeAdopter) ApplyAdoption(ctx context.Context, req AdoptRequest) (AdoptResponse, error) {
	tokenHash := internalapiTokenSHA256Hex(req.APIToken)
	if _, err := os.Stat(a.AdoptionPath); err == nil {
		existing, readErr := readAdoptionState(a.AdoptionPath)
		if readErr != nil {
			return AdoptResponse{}, fmt.Errorf("read existing adoption config: %w", readErr)
		}
		if existing.TokenSHA256 == tokenHash &&
			existing.NUTUser == req.NUTUser &&
			existing.NUTPassword == req.NUTPassword &&
			existing.ControllerURL == req.ControllerURL {
			if a.Logger != nil {
				a.Logger.Printf("adoption replay accepted serial=%s controller=%s", a.Serial, req.ControllerURL)
			}
			tlsPort := existing.TLSPort
			if tlsPort == 0 {
				tlsPort = a.TLSPort
			}
			fingerprint := existing.TLSFingerprint
			if fingerprint == "" {
				var certErr error
				fingerprint, certErr = ensureNodeCertificate(a.Serial, a.TLSCertPath, a.TLSKeyPath)
				if certErr != nil {
					return AdoptResponse{}, fmt.Errorf("ensure node TLS certificate: %w", certErr)
				}
			}
			if existing.TLSPort != tlsPort || existing.TLSFingerprint != fingerprint {
				existing.TLSPort = tlsPort
				existing.TLSFingerprint = fingerprint
				if writeErr := writeAdoptionState(a.AdoptionPath, existing); writeErr != nil {
					return AdoptResponse{}, fmt.Errorf("write adoption config: %w", writeErr)
				}
			}
			return AdoptResponse{
				Serial:         a.Serial,
				Version:        a.Version,
				ControllerURL:  req.ControllerURL,
				TLSPort:        tlsPort,
				TLSFingerprint: fingerprint,
				TokenSHA256:    existing.TokenSHA256,
			}, nil
		}
		return AdoptResponse{}, fmt.Errorf("%w: %s", ErrNodeAlreadyAdopted, a.Serial)
	} else if !errors.Is(err, os.ErrNotExist) {
		return AdoptResponse{}, fmt.Errorf("stat adoption config: %w", err)
	}

	state := adoptionState{
		CAPEM:         req.CAPEM,
		NUTUser:       req.NUTUser,
		NUTPassword:   req.NUTPassword,
		TokenSHA256:   tokenHash,
		ControllerURL: req.ControllerURL,
		TLSPort:       a.TLSPort,
		AdoptedAt:     time.Now().UTC(),
	}
	fingerprint, err := ensureNodeCertificate(a.Serial, a.TLSCertPath, a.TLSKeyPath)
	if err != nil {
		return AdoptResponse{}, fmt.Errorf("ensure node TLS certificate: %w", err)
	}
	state.TLSFingerprint = fingerprint
	if err := os.MkdirAll(filepath.Dir(a.AdoptionPath), 0o755); err != nil {
		return AdoptResponse{}, fmt.Errorf("create adoption dir: %w", err)
	}
	if err := writeAdoptionState(a.AdoptionPath, state); err != nil {
		return AdoptResponse{}, fmt.Errorf("write adoption config: %w", err)
	}

	if _, err := nutconf.WriteIfChanged(filepath.Join(a.ConfigDir, "upsd.users"), nutconf.RenderUPSDUsers(nutconf.UPSDUser{Username: req.NUTUser, Password: req.NUTPassword})); err != nil {
		return AdoptResponse{}, fmt.Errorf("write upsd.users: %w", err)
	}
	if a.Reloader != nil {
		if err := a.Reloader.Reload(ctx, true, nil); err != nil {
			return AdoptResponse{}, fmt.Errorf("reload NUT services: %w", err)
		}
	}
	if a.Inventory != nil {
		a.Inventory.UpdateNUTCredentials(req.NUTUser, req.NUTPassword)
	}
	if a.Advertiser != nil {
		a.Advertiser.UpdateAdopted(true)
	}

	return AdoptResponse{
		Serial:         a.Serial,
		Version:        a.Version,
		ControllerURL:  req.ControllerURL,
		TLSPort:        a.TLSPort,
		TLSFingerprint: fingerprint,
		TokenSHA256:    internalapiTokenSHA256Hex(req.APIToken),
	}, nil
}

func readAdoptionState(path string) (adoptionState, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return adoptionState{}, err
	}
	var state adoptionState
	if err := json.Unmarshal(content, &state); err != nil {
		return adoptionState{}, err
	}
	return state, nil
}

func writeAdoptionState(path string, state adoptionState) error {
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

func DynamicCertificateLoader(certPath, keyPath string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("TLS certificate paths unavailable")
		}
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, err
		}
		return &certificate, nil
	}
}

func ensureNodeCertificate(serial, certPath, keyPath string) (string, error) {
	if _, certErr := os.Stat(certPath); certErr == nil {
		if _, keyErr := os.Stat(keyPath); keyErr == nil {
			return certificateFingerprint(certPath)
		}
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return "", fmt.Errorf("create TLS dir: %w", err)
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate node TLS key: %w", err)
	}
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", fmt.Errorf("generate node TLS serial: %w", err)
	}
	certificateTemplate := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: "wattkeeper-node-" + serial},
		NotBefore:             time.Now().Add(-1 * time.Hour).UTC(),
		NotAfter:              time.Now().AddDate(5, 0, 0).UTC(),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", fmt.Errorf("create node TLS certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("marshal node TLS key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return "", fmt.Errorf("write node TLS cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", fmt.Errorf("write node TLS key: %w", err)
	}
	return certificateFingerprint(certPath)
}

func certificateFingerprint(certPath string) (string, error) {
	content, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("read node TLS cert: %w", err)
	}
	block, _ := pem.Decode(content)
	if block == nil {
		return "", fmt.Errorf("decode node TLS cert PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	return fmt.Sprintf("%x", sum[:]), nil
}

func internalapiTokenSHA256Hex(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}
