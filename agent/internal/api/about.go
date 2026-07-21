package api

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	osReleasePath   = "etc/os-release"
	dpkgStatusPath  = "var/lib/dpkg/status"
	kernelReleasePath = "proc/sys/kernel/osrelease"
)

type aboutResponse struct {
	Version        string            `json:"version"`
	Serial         string            `json:"serial"`
	Hostname       string            `json:"hostname"`
	UptimeSeconds  int64             `json:"uptime_seconds"`
	OperatingSystem operatingSystem  `json:"operating_system"`
	Kernel         string            `json:"kernel"`
	Architecture   string            `json:"architecture"`
	GoVersion      string            `json:"go_version"`
	Adopted        bool              `json:"adopted"`
	Featured       []aboutDependency `json:"featured_dependencies"`
	GoModules      []aboutDependency `json:"go_modules"`
	DebianPackages []debianPackage   `json:"debian_packages"`
	Warnings       []string          `json:"warnings,omitempty"`
}

type operatingSystem struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type aboutDependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
}

type debianPackage struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture,omitempty"`
}

func (s *Service) buildAboutResponse() aboutResponse {
	response := aboutResponse{
		Version:       s.version,
		Serial:        s.serial,
		UptimeSeconds: int64(timeSince(s.startedAt).Seconds()),
		Architecture:  runtime.GOARCH,
		GoVersion:     runtime.Version(),
	}

	if hostname, err := os.Hostname(); err == nil {
		response.Hostname = hostname
	} else {
		response.Warnings = append(response.Warnings, "hostname is unavailable")
	}

	osRelease, err := readOSRelease(filepath.Join(s.rootPath, osReleasePath))
	if err != nil {
		response.Warnings = append(response.Warnings, "operating system metadata is unavailable")
	} else {
		response.OperatingSystem = operatingSystem{Name: osRelease["PRETTY_NAME"], Version: osRelease["VERSION_ID"]}
		if response.OperatingSystem.Name == "" {
			response.OperatingSystem.Name = osRelease["NAME"]
		}
		if response.OperatingSystem.Name != "" {
			response.Featured = append(response.Featured, aboutDependency{Name: response.OperatingSystem.Name, Version: response.OperatingSystem.Version, Source: "https://www.debian.org/"})
		}
	}

	if kernel, err := os.ReadFile(filepath.Join(s.rootPath, kernelReleasePath)); err == nil {
		response.Kernel = strings.TrimSpace(string(kernel))
		if response.Kernel != "" {
			response.Featured = append(response.Featured, aboutDependency{Name: "Linux kernel", Version: response.Kernel, Source: "https://kernel.org/"})
		}
	} else {
		response.Warnings = append(response.Warnings, "kernel metadata is unavailable")
	}

	response.GoModules, response.Featured = buildGoDependencies(s.version)
	if len(response.GoModules) == 0 {
		response.Warnings = append(response.Warnings, "compiled Go dependency metadata is unavailable")
	}

	packages, err := readDebianPackages(filepath.Join(s.rootPath, dpkgStatusPath))
	if err != nil {
		response.Warnings = append(response.Warnings, "installed package metadata is unavailable")
	} else {
		response.DebianPackages = packages
		response.Featured = append(response.Featured, featuredRuntimePackages(packages)...)
	}

	adoption, err := s.loadAdoption()
	if err != nil {
		response.Warnings = append(response.Warnings, "controller adoption status is unavailable")
	} else {
		response.Adopted = adoption != nil
	}

	sort.Slice(response.Featured, func(i, j int) bool { return response.Featured[i].Name < response.Featured[j].Name })
	return response
}

func timeSince(startedAt time.Time) time.Duration {
	return time.Since(startedAt)
}

func readOSRelease(path string) (map[string]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("parse %s value: %w", key, err)
			}
			value = unquoted
		}
		values[key] = value
	}
	return values, nil
}

func readDebianPackages(path string) ([]debianPackage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var packages []debianPackage
	fields := make(map[string]string)
	appendPackage := func() {
		if fields["Status"] == "install ok installed" && fields["Package"] != "" && fields["Version"] != "" {
			packages = append(packages, debianPackage{Name: fields["Package"], Version: fields["Version"], Architecture: fields["Architecture"]})
		}
		fields = make(map[string]string)
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			appendPackage()
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found {
			fields[key] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	appendPackage()
	sort.Slice(packages, func(i, j int) bool { return packages[i].Name < packages[j].Name })
	return packages, nil
}

func buildGoDependencies(version string) ([]aboutDependency, []aboutDependency) {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, nil
	}

	modules := make([]aboutDependency, 0, len(buildInfo.Deps)+1)
	if buildInfo.Main.Path != "" {
		modules = append(modules, moduleDependency(buildInfo.Main, version))
	}
	for _, module := range buildInfo.Deps {
		modules = append(modules, moduleDependency(*module, ""))
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })

	featuredNames := map[string]bool{
		"github.com/Foehammer82/strom/agent": true,
		"github.com/grandcat/zeroconf":         true,
		"gopkg.in/yaml.v3":                      true,
	}
	featured := make([]aboutDependency, 0, len(featuredNames))
	for _, dependency := range modules {
		if featuredNames[dependency.Name] {
			featured = append(featured, dependency)
		}
	}
	return modules, featured
}

func moduleDependency(module debug.Module, fallbackVersion string) aboutDependency {
	version := module.Version
	if version == "" || version == "(devel)" {
		version = fallbackVersion
	}
	if module.Replace != nil && module.Replace.Version != "" {
		version = module.Replace.Version
	}
	return aboutDependency{Name: module.Path, Version: version, Source: "https://pkg.go.dev/" + module.Path}
}

func featuredRuntimePackages(packages []debianPackage) []aboutDependency {
	featuredNames := map[string]string{
		"nut-server": "Network UPS Tools",
		"systemd":    "systemd",
	}
	var featured []aboutDependency
	for _, pkg := range packages {
		if name, ok := featuredNames[pkg.Name]; ok {
			featured = append(featured, aboutDependency{Name: name, Version: pkg.Version, Source: "https://www.debian.org/"})
		}
	}
	return featured
}
