package serve

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	jjconfig "github.com/jungju/jj/internal/config"
)

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 7331
)

func ResolveConfig(cfg Config) (Config, error) {
	if err := loadServeEnvFiles(cfg, false); err != nil {
		return cfg, err
	}
	fileCfg, err := loadProjectConfig(cfg)
	if err != nil {
		return cfg, err
	}
	if strings.TrimSpace(fileCfg.Path) != "" {
		cfg.ConfigFile = fileCfg.Path
	}

	if !cfg.CWDExplicit {
		if v := firstEnv("JJ_CWD", "JJ_WORKSPACE_CWD"); v != "" {
			cfg.CWD = v
		} else if v := strings.TrimSpace(fileCfg.CWD); v != "" {
			cfg.CWD = v
		}
	}
	if err := loadServeEnvFiles(cfg, true); err != nil {
		return cfg, err
	}
	applyServeEnvAliases()

	envAddr := firstEnv("JJ_SERVE_ADDR", "JJ_SERVER_ADDR", "JJ_ADDR")
	envHost := firstEnv("JJ_SERVE_HOST", "JJ_SERVER_HOST", "JJ_HOST")
	envPort := firstEnv("JJ_SERVE_PORT", "JJ_SERVER_PORT", "JJ_PORT")

	switch {
	case cfg.AddrExplicit:
		if strings.TrimSpace(cfg.Addr) == "" {
			cfg.Addr = DefaultAddr
		}
	case strings.TrimSpace(cfg.Addr) != "" && cfg.Addr != DefaultAddr:
		// Programmatic callers often pass a fully resolved address without
		// CLI-style explicit markers.
	case cfg.HostExplicit || cfg.PortExplicit:
		host := cfg.Host
		if strings.TrimSpace(host) == "" {
			host = defaultHostFrom(envHost, fileCfg)
		}
		port := cfg.Port
		if !cfg.PortExplicit {
			parsed, err := defaultPortFrom(envPort, fileCfg)
			if err != nil {
				return cfg, err
			}
			port = parsed
		}
		cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	case envAddr != "":
		cfg.Addr = envAddr
	case strings.TrimSpace(fileCfg.ServeAddr) != "":
		cfg.Addr = fileCfg.ServeAddr
	default:
		host := defaultHostFrom(envHost, fileCfg)
		port, err := defaultPortFrom(envPort, fileCfg)
		if err != nil {
			return cfg, err
		}
		cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = DefaultAddr
	}
	return cfg, nil
}

func loadProjectConfig(cfg Config) (jjconfig.File, error) {
	if strings.TrimSpace(cfg.ConfigFile) != "" {
		return jjconfig.Load(cfg.ConfigFile)
	}
	if cfg.CWDExplicit && strings.TrimSpace(cfg.CWD) != "" {
		if info, statErr := os.Stat(cfg.CWD); statErr == nil && info.IsDir() {
			fileCfg, found, err := jjconfig.LoadFrom(cfg.CWD)
			if err != nil || found {
				return fileCfg, err
			}
		}
	}
	if !cfg.CWDExplicit {
		if envCWD := firstEnv("JJ_CWD", "JJ_WORKSPACE_CWD"); envCWD != "" {
			if info, statErr := os.Stat(envCWD); statErr == nil && info.IsDir() {
				fileCfg, found, err := jjconfig.LoadFrom(envCWD)
				if err != nil || found {
					return fileCfg, err
				}
			}
		}
	}
	if strings.TrimSpace(cfg.ConfigSearchDir) != "" {
		fileCfg, found, err := jjconfig.LoadFrom(cfg.ConfigSearchDir)
		if err != nil || found {
			return fileCfg, err
		}
	}
	if strings.TrimSpace(cfg.CWD) != "" {
		if info, statErr := os.Stat(cfg.CWD); statErr == nil && info.IsDir() {
			fileCfg, _, err := jjconfig.LoadFrom(cfg.CWD)
			return fileCfg, err
		}
	}
	return jjconfig.File{}, nil
}

func defaultHostFrom(envHost string, fileCfg jjconfig.File) string {
	if strings.TrimSpace(envHost) != "" {
		return envHost
	}
	if strings.TrimSpace(fileCfg.ServeHost) != "" {
		return fileCfg.ServeHost
	}
	return DefaultHost
}

func defaultPortFrom(envPort string, fileCfg jjconfig.File) (int, error) {
	if strings.TrimSpace(envPort) != "" {
		port, err := strconv.Atoi(envPort)
		if err != nil {
			return 0, fmt.Errorf("parse JJ_SERVE_PORT: %w", err)
		}
		return port, nil
	}
	if fileCfg.ServePort != nil {
		return *fileCfg.ServePort, nil
	}
	return DefaultPort, nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
