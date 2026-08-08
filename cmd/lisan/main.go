package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/cliout"
	"lisanalgaib/internal/configui"
	"lisanalgaib/internal/deps"
	"lisanalgaib/internal/extensionbundle"
	"lisanalgaib/internal/installer"
	"lisanalgaib/internal/launcher"
	"lisanalgaib/internal/lifecycle"
	"lisanalgaib/internal/teaprogram"
	"lisanalgaib/internal/ui"
)

type command string

const (
	commandRun       command = "run"
	commandDocker    command = "docker"
	commandVM        command = "vm"
	commandConfig    command = "config"
	commandCleanup   command = "cleanup"
	commandInstall   command = "install"
	commandUninstall command = "uninstall"
	commandHelp      command = "help"
)

type options struct {
	command   command
	workspace string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		cliout.Failure(os.Stderr, "Lisan", err)
		os.Exit(1)
	}
}

func run(arguments []string) (resultErr error) {
	insideContainer := os.Getenv("LISAN_CONTAINER") == "1"
	options, err := parseOptions(arguments, insideContainer)
	if err != nil {
		return err
	}
	if options.command == commandHelp {
		printUsage(os.Stdout)
		return nil
	}
	if options.command == commandInstall {
		binary, err := installer.Install(os.Stdout)
		if err != nil {
			return err
		}
		if advice := installer.PathAdvice(binary); advice != "" {
			cliout.Detail(os.Stdout, "PATH", advice)
		}
		return nil
	}
	if options.command == commandUninstall {
		return installer.Uninstall(os.Stdout)
	}

	ctx, stopSignals := lifecycle.NotifyContext(context.Background())
	defer stopSignals()

	if options.command == commandCleanup {
		return launcher.Cleanup(ctx, launcher.CleanupOptions{Stdout: os.Stdout, Stderr: os.Stderr})
	}

	document, profile, configPath, err := appconfig.LoadActive()
	if err != nil {
		return err
	}
	if !insideContainer {
		runtimeRoot, rootErr := extensionbundle.FindRoot(document.RuntimeRoot)
		if rootErr != nil {
			return rootErr
		}
		bundles, discoverErr := extensionbundle.Discover(runtimeRoot)
		if discoverErr != nil {
			return discoverErr
		}
		extensionbundle.Reconcile(&document, bundles)
		if active, ok := document.Active(); ok {
			profile = active
		}
	}
	if options.command == commandConfig {
		_, _, err := configui.Run(document, configPath)
		return err
	}
	if options.command == commandDocker {
		if err := deps.EnsureDocker(ctx, os.Stderr); err != nil {
			return err
		}
		return launcher.RunDocker(ctx, launcher.DockerOptions{
			RuntimeRoot: document.RuntimeRoot,
			Workspace:   options.workspace,
			Profile:     profile,
			Stdin:       os.Stdin,
			Stdout:      os.Stdout,
			Stderr:      os.Stderr,
		})
	}
	wormsign := options.command == commandVM
	if wormsign {
		// The vm command currently enters Wormsign and executes as the host user.
		if err := os.Setenv("LISAN_WORMSIGN", "1"); err != nil {
			return err
		}
		runtimeRoot := document.RuntimeRoot
		if runtimeRoot == "" {
			runtimeRoot, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("locate source runtime: %w", err)
			}
		}
		sharedRoot, err := launcher.PrepareSharedDirectory(runtimeRoot, document.RuntimeRoot != "")
		if err != nil {
			return err
		}
		if err := os.Setenv("LISAN_SHARED_DIR", sharedRoot); err != nil {
			return err
		}
		if profile.Feature("files") || profile.Feature("agents") || profile.Tool("nvchad") {
			if err := installer.SeedUserAssets(runtimeRoot, profile); err != nil {
				return fmt.Errorf("prepare configured user assets: %w", err)
			}
		}
		if err := deps.Ensure(ctx, profile, os.Stderr); err != nil {
			return err
		}
		nativeProfile, nativeRuntime, err := launcher.StartNativeConnectors(ctx, runtimeRoot, profile, os.Stderr)
		if err != nil {
			return err
		}
		profile = nativeProfile
		defer func() {
			resultErr = errors.Join(resultErr, nativeRuntime.Close())
		}()
	}
	root, err := workspaceRoot(options.workspace, wormsign)
	if err != nil {
		return err
	}
	return runTUI(ctx, root, profile)
}

func parseOptions(arguments []string, insideContainer bool) (options, error) {
	if len(arguments) == 0 {
		return options{command: commandHelp}, nil
	}
	if len(arguments) == 1 && (arguments[0] == "-h" || arguments[0] == "--help") {
		return options{command: commandHelp}, nil
	}
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-") {
			return options{}, fmt.Errorf("flags are not supported; run 'lisan help' for word commands")
		}
	}
	parsed := options{command: command(arguments[0])}
	switch parsed.command {
	case commandDocker, commandVM:
		if len(arguments) > 2 {
			return options{}, fmt.Errorf("usage: lisan %s [workspace]", parsed.command)
		}
		if len(arguments) == 2 {
			parsed.workspace = arguments[1]
		}
	case commandRun:
		if !insideContainer {
			return options{}, errors.New("run is an internal container command; use docker or vm")
		}
		if len(arguments) > 2 {
			return options{}, errors.New("usage: lisan run [workspace]")
		}
		if len(arguments) == 2 {
			parsed.workspace = arguments[1]
		}
	case commandConfig, commandCleanup, commandInstall, commandUninstall, commandHelp:
		if len(arguments) != 1 {
			return options{}, fmt.Errorf("%s does not accept additional arguments", parsed.command)
		}
	default:
		return options{}, fmt.Errorf("unknown command %q; run 'lisan help'", arguments[0])
	}
	return parsed, nil
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, `LisanAlGaib ╾━━━━━━━━━━━━━━━━━━━━╼ help

Usage: lisan [command] [workspace]

Commands:
  docker [workspace]  launch the configured Docker workspace
  vm [workspace]      launch Wormsign as the current host user (unsandboxed)
  config              edit and activate profiles
  cleanup             remove owned Docker state; preserve shared files
  install             install this executable and its embedded runtime
  uninstall           remove the executable/runtime; preserve config and data
  help, -h, --help    show this help

Running lisan without a command prints this help. Interactive interfaces are
entered only through config, vm, or docker.`)
}

func workspaceRoot(configured string, wormsign bool) (string, error) {
	root := configured
	if root == "" {
		var err error
		if wormsign {
			root, err = os.UserHomeDir()
		} else {
			root, err = os.Getwd()
		}
		if err != nil {
			return "", fmt.Errorf("determine workspace: %w", err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("invalid workspace: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("inspect workspace %s: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory: %s", root)
	}
	return root, nil
}

func runTUI(ctx context.Context, root string, profile appconfig.Profile) error {
	model := ui.NewModelWithProfile(root, profile)
	defer model.Close()
	if _, err := teaprogram.Run(model, tea.WithContext(ctx)); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}
