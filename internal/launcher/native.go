package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/extensionhost"
)

// NativeRuntime owns only extension hosts started for the current Wormsign
// launch. Their endpoints live in the returned in-memory profile and never
// replace the Docker endpoints saved in config.
type NativeRuntime struct {
	hosts []*extensionhost.Host
}

func StartNativeConnectors(ctx context.Context, runtimeRoot string, profile appconfig.Profile, output io.Writer) (appconfig.Profile, *NativeRuntime, error) {
	validated, err := appconfig.NormalizeProfile(profile)
	if err != nil {
		return profile, nil, err
	}
	profile = validated
	active := profile.Clone()
	runtime := &NativeRuntime{}
	fail := func(err error) (appconfig.Profile, *NativeRuntime, error) {
		return profile, nil, errors.Join(err, runtime.Close())
	}
	for index := range active.Connectors {
		connector := &active.Connectors[index]
		if !connector.Enabled || !connector.Managed {
			continue
		}
		if strings.TrimSpace(connector.NativeConfig) == "" {
			return fail(fmt.Errorf("managed extension %s has no native_config for vm mode", connector.ID))
		}
		configPath, err := resolveRuntimePath(runtimeRoot, connector.NativeConfig)
		if err != nil {
			return fail(fmt.Errorf("native extension %s config: %w", connector.ID, err))
		}
		config, err := extensionhost.LoadConfig(configPath)
		if err != nil {
			return fail(fmt.Errorf("native extension %s: %w", connector.ID, err))
		}
		if config.Manifest.ID != connector.ID {
			return fail(fmt.Errorf("native extension config id %q does not match profile id %q", config.Manifest.ID, connector.ID))
		}
		host, err := extensionhost.StartNative(ctx, configPath)
		if err != nil {
			return fail(err)
		}
		runtime.hosts = append(runtime.hosts, host)
		connector.Endpoint = host.Endpoint()
		connector.Network = "native-loopback"
		if output != nil {
			fmt.Fprintf(output, "Native extension %s listening on %s\n", connector.Name, connector.Endpoint)
		}
	}
	return active, runtime, nil
}

func (r *NativeRuntime) Close() error {
	if r == nil {
		return nil
	}
	var result error
	for index := len(r.hosts) - 1; index >= 0; index-- {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := r.hosts[index].Close(ctx)
		cancel()
		result = errors.Join(result, err)
	}
	r.hosts = nil
	return result
}
